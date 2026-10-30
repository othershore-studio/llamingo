// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

// fakeHTTPMCP is a minimal Streamable HTTP MCP server for tests. It answers
// initialize/tools/list/tools/call; sse toggles the two response framings the
// transport must handle (a single JSON body vs. an event stream). It records the
// auth header, session id, and protocol-version header it saw, so the test can
// assert the client sends them back.
type fakeHTTPMCP struct {
	sse         bool
	sawAuth     string
	sawSession  string
	sawProtocol string
}

func (f *fakeHTTPMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if v := r.Header.Get("Authorization"); v != "" {
		f.sawAuth = v
	}
	if v := r.Header.Get("Mcp-Session-Id"); v != "" {
		f.sawSession = v
	}
	if v := r.Header.Get("MCP-Protocol-Version"); v != "" {
		f.sawProtocol = v
	}

	var req struct {
		ID     *int   `json:"id"`
		Method string `json:"method"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted) // a notification wants no reply
		return
	}

	var result any
	switch req.Method {
	case "initialize":
		w.Header().Set("Mcp-Session-Id", "sess-123")
		result = map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]any{"name": "fake", "version": "1"},
		}
	case "tools/list":
		result = map[string]any{"tools": []any{map[string]any{
			"name": "echo", "description": "echoes", "inputSchema": map[string]any{"type": "object"},
		}}}
	case "tools/call":
		result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "pong"}}}
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
		return
	}

	payload, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	if !f.sse {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
		return
	}
	// Event-stream framing: a keep-alive comment and an unrelated server
	// notification precede the reply, to prove the transport skips them.
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, ": keep-alive\n\n")
	_, _ = io.WriteString(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{}}\n\n")
	_, _ = io.WriteString(w, "data: "+string(payload)+"\n\n")
}

// A Streamable HTTP server reached end to end, in both response framings: the
// handshake, a tools/list, and a tools/call, with the auth/session/protocol
// headers echoed back as the spec requires.
func TestHTTPClientRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name string
		sse  bool
	}{{"json", false}, {"sse", true}} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeHTTPMCP{sse: tc.sse}
			srv := httptest.NewServer(fake)
			defer srv.Close()

			c := mcp.NewHTTPClient(srv.URL, map[string]string{"Authorization": "Bearer tok"})
			defer c.Close()

			ctx := context.Background()
			if err := c.Initialize(ctx); err != nil {
				t.Fatalf("initialize: %v", err)
			}
			tools, err := c.ListTools(ctx)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(tools) != 1 || tools[0].Name != "echo" {
				t.Fatalf("tools = %+v, want one named 'echo'", tools)
			}
			res, err := c.CallTool(ctx, "echo", map[string]any{"x": 1})
			if err != nil {
				t.Fatalf("call: %v", err)
			}
			if got := res.Text(); got != "pong" {
				t.Fatalf("CallTool = %q, want %q", got, "pong")
			}

			if fake.sawAuth != "Bearer tok" {
				t.Errorf("auth header = %q, want %q", fake.sawAuth, "Bearer tok")
			}
			if fake.sawSession != "sess-123" {
				t.Errorf("session id echoed = %q, want sess-123 (from initialize)", fake.sawSession)
			}
			if fake.sawProtocol != "2025-06-18" {
				t.Errorf("protocol header = %q, want 2025-06-18 (sent after initialize)", fake.sawProtocol)
			}
		})
	}
}

// Connect routes an http server to the Streamable HTTP transport end to end.
func TestConnectHTTP(t *testing.T) {
	srv := httptest.NewServer(&fakeHTTPMCP{})
	defer srv.Close()

	s := mcp.Server{Name: "remote", Scope: mcp.ScopeGlobal, Config: mcp.ServerConfig{Type: "http", URL: srv.URL}}
	c, err := mcp.Connect(s)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Close()
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

// An http server configured without a URL is a config error, caught at Connect.
func TestConnectHTTPNoURL(t *testing.T) {
	s := mcp.Server{Name: "remote", Scope: mcp.ScopeGlobal, Config: mcp.ServerConfig{Type: "http"}}
	if _, err := mcp.Connect(s); err == nil {
		t.Fatal("Connect to an http server with no url should error")
	}
}

// A Streamable HTTP server may drop a session (it expires, or the server
// restarts); a later request carrying the dead session id gets HTTP 404
// "Session not found". The client must recover by starting a new session and
// retrying — not surface the 404 as a tool error. This fake 404s any call made
// with the first session it issued, and accepts calls made with a later one, so
// recovery requires a genuine re-initialize.
func TestHTTPReinitializesAfterSessionExpiry(t *testing.T) {
	var (
		mu           sync.Mutex
		initCount    int
		firstSession string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)

		switch req.Method {
		case "initialize":
			mu.Lock()
			initCount++
			sid := fmt.Sprintf("sess-%d", initCount)
			if firstSession == "" {
				firstSession = sid
			}
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", sid)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			mu.Lock()
			first := firstSession
			mu.Unlock()
			if r.Header.Get("Mcp-Session-Id") == first {
				// the session the client first established is now gone
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Session not found"}}`)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"content":[{"type":"text","text":"forecast: sunny"}]}}`, *req.ID)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mcp.NewHTTPClient(srv.URL, nil)
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// The first tools/call rides the now-dead session → 404; the transport must
	// re-initialize and retry rather than return the error to the model.
	res, err := c.CallTool(ctx, "get_forecast", map[string]any{"location": "Quito"})
	if err != nil {
		t.Fatalf("CallTool should recover from a dropped session, got: %v", err)
	}
	if res.Text() != "forecast: sunny" {
		t.Errorf("result = %q, want %q", res.Text(), "forecast: sunny")
	}
	mu.Lock()
	got := initCount
	mu.Unlock()
	if got != 2 {
		t.Errorf("initialize called %d times, want 2 (initial handshake + re-init after the 404)", got)
	}
}

// A 404 that is NOT a session problem (a wrong URL, a gone endpoint) must surface
// as an error, not trigger a pointless re-initialize + retry — a bare 404 would
// otherwise mask a config mistake.
func TestHTTPNonSessionNotFoundSurfaces(t *testing.T) {
	var (
		mu        sync.Mutex
		initCount int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		switch req.Method {
		case "initialize":
			mu.Lock()
			initCount++
			mu.Unlock()
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}`, *req.ID)
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, "404 page not found") // no mention of a session
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	c := mcp.NewHTTPClient(srv.URL, nil)
	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	_, err := c.CallTool(ctx, "get_forecast", map[string]any{})
	if err == nil {
		t.Fatal("a non-session 404 should surface as an error, not be retried away")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error = %v, want it to mention the 404", err)
	}
	mu.Lock()
	got := initCount
	mu.Unlock()
	if got != 1 {
		t.Errorf("initialize called %d times, want 1 (no spurious re-init on a non-session 404)", got)
	}
}

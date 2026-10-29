// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

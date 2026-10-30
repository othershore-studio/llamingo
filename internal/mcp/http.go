// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// httpTransport speaks MCP Streamable HTTP: each JSON-RPC message is POSTed to a
// single endpoint, and the server answers either with one application/json
// response or a text/event-stream carrying the response (plus, possibly, other
// messages we skip). The server may hand out an Mcp-Session-Id on initialize; we
// echo it, and the negotiated protocol version, on every later request. See the
// MCP spec, "Transports → Streamable HTTP". This is what Claude Desktop and the
// mcpo gateway expose, so an HTTP MCP server reaches llaminGO the same way it
// reaches Claude.
type httpTransport struct {
	url     string
	headers map[string]string
	client  *http.Client

	mu         sync.Mutex
	next       int
	sessionID  string // handed back on initialize, sent on every later request
	protocol   string // negotiated protocol version, sent after initialize
	initParams any    // the initialize params, kept so a dropped session can be re-established
}

func newHTTPTransport(url string, headers map[string]string) *httpTransport {
	return &httpTransport{url: url, headers: headers, client: &http.Client{}}
}

// NewHTTPClient wraps a Streamable HTTP MCP server as a client. headers are sent
// on every request (e.g. {"Authorization": "Bearer …"} for a gated server).
func NewHTTPClient(url string, headers map[string]string) *Client {
	return &Client{tr: newHTTPTransport(url, headers)}
}

// Close is a no-op: HTTP keeps no long-lived connection of its own.
func (t *httpTransport) Close() error { return nil }

func (t *httpTransport) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == "initialize" {
		// Remember the handshake so a later-expired session can be re-established.
		t.mu.Lock()
		t.initParams = params
		t.mu.Unlock()
	}
	// A non-initialize call may retry once, after re-establishing a dropped session.
	return t.callWithRetry(ctx, method, params, method != "initialize")
}

func (t *httpTransport) callWithRetry(ctx context.Context, method string, params any, canRetry bool) (json.RawMessage, error) {
	t.mu.Lock()
	t.next++
	id := t.next
	hadSession := t.sessionID != ""
	t.mu.Unlock()

	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal %s: %w", method, err)
	}
	resp, err := t.post(ctx, body)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", method, err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		t.mu.Lock()
		t.sessionID = sid
		t.mu.Unlock()
	}
	// A 404 whose body names a session problem ("Session not found", an unknown
	// Mcp-Session-Id) means the server dropped our session — it expired, or the
	// server restarted. Per the MCP Streamable HTTP spec, start a new session:
	// re-run the handshake and retry once. We gate on the session signal, not the
	// bare 404, so a wrong URL or a gone endpoint surfaces as an error instead of
	// a pointless re-init + retry (a 404 otherwise hides auth/URL mistakes).
	if resp.StatusCode == http.StatusNotFound && canRetry && hadSession {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if isSessionError(msg) {
			if err := t.reinitialize(ctx); err != nil {
				return nil, fmt.Errorf("mcp: %s: session expired and re-initialize failed: %w", method, err)
			}
			return t.callWithRetry(ctx, method, params, false)
		}
		return nil, fmt.Errorf("mcp: %s: http 404: %s", method, strings.TrimSpace(string(msg)))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("mcp: %s: http %d: %s", method, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var result json.RawMessage
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		result, err = readSSEResponse(ctx, resp.Body, id)
	} else {
		result, err = readJSONResponse(resp.Body, id)
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", method, err)
	}
	if method == "initialize" {
		t.captureProtocol(result)
	}
	return result, nil
}

// reinitialize re-runs the MCP handshake after the server dropped our session,
// so a retried request starts a fresh one. It clears the dead session (and stale
// protocol) first, so the initialize POST carries no stale Mcp-Session-Id.
func (t *httpTransport) reinitialize(ctx context.Context) error {
	t.mu.Lock()
	t.sessionID, t.protocol = "", ""
	params := t.initParams
	t.mu.Unlock()
	if params == nil {
		return fmt.Errorf("no initialize params captured")
	}
	if _, err := t.callWithRetry(ctx, "initialize", params, false); err != nil {
		return err
	}
	return t.notify("notifications/initialized", nil)
}

// isSessionError reports whether a 404 body indicates an expired or unknown MCP
// session rather than, say, a wrong URL. The Streamable HTTP session signal
// names the session ("Session not found", an unknown Mcp-Session-Id), so a body
// mentioning "session" is the retry trigger; a plain 404 page is not.
func isSessionError(body []byte) bool {
	return bytes.Contains(bytes.ToLower(body), []byte("session"))
}

func (t *httpTransport) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return fmt.Errorf("mcp: marshal notify %s: %w", method, err)
	}
	resp, err := t.post(context.Background(), body)
	if err != nil {
		return fmt.Errorf("mcp: notify %s: %w", method, err)
	}
	// A notification expects no JSON-RPC reply; drain and close so the
	// connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: notify %s: http %d", method, resp.StatusCode)
	}
	return nil
}

func (t *httpTransport) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// Caller-supplied headers first (bearer token etc.); the protocol headers
	// below are ours to set and must not be overridden.
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	t.mu.Lock()
	sid, proto := t.sessionID, t.protocol
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if proto != "" {
		req.Header.Set("MCP-Protocol-Version", proto)
	}
	return t.client.Do(req)
}

func (t *httpTransport) captureProtocol(result json.RawMessage) {
	var out struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(result, &out) == nil && out.ProtocolVersion != "" {
		t.mu.Lock()
		t.protocol = out.ProtocolVersion
		t.mu.Unlock()
	}
}

// readJSONResponse reads a single application/json JSON-RPC response for id.
func readJSONResponse(r io.Reader, id int) (json.RawMessage, error) {
	var resp rpcResponse
	if err := json.NewDecoder(r).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if resp.ID == nil || *resp.ID != id {
		return nil, fmt.Errorf("response id %v does not match request %d", resp.ID, id)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

// readSSEResponse scans an SSE stream for the JSON-RPC response to id, skipping
// any server-initiated messages that arrive first. Data lines within one event
// are joined with newlines per the SSE spec before being parsed as JSON-RPC.
func readSSEResponse(ctx context.Context, r io.Reader, id int) (json.RawMessage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var data strings.Builder
	dispatch := func() (json.RawMessage, bool, error) {
		if data.Len() == 0 {
			return nil, false, nil
		}
		raw := data.String()
		data.Reset()
		var resp rpcResponse
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			return nil, false, nil // not a message we can read; skip the event
		}
		if resp.ID == nil || *resp.ID != id {
			return nil, false, nil // server request/notification, or another id
		}
		if resp.Error != nil {
			return nil, false, resp.Error
		}
		return resp.Result, true, nil
	}
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := sc.Text()
		switch {
		case line == "":
			if res, done, err := dispatch(); err != nil || done {
				return res, err
			}
		case strings.HasPrefix(line, ":"):
			// comment / keep-alive
		case strings.HasPrefix(line, "data:"):
			v := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(v)
		default:
			// event:, id:, retry: — not needed to route a JSON-RPC reply
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read sse: %w", err)
	}
	// A final event may not be terminated by a blank line before EOF.
	if res, done, err := dispatch(); err != nil || done {
		return res, err
	}
	return nil, fmt.Errorf("stream ended before a response to request %d", id)
}

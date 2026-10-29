// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

// TestMain lets this test binary double as a tiny MCP server when the env flag
// is set — the standard way to exercise an exec-based transport cross-platform
// without depending on an external server.
func TestMain(m *testing.M) {
	if os.Getenv("LLAMINGO_MCP_HELPER") == "1" {
		runHelperMCPServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// runHelperMCPServer answers the minimal MCP handshake over stdin/stdout: an
// initialize, a tools/list with one "echo" tool, and a tools/call that echoes
// its "text" argument.
func runHelperMCPServer() {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := dec.Decode(&req); err != nil {
			return // stdin closed: the client is done
		}
		if req.ID == nil {
			continue // a notification (e.g. notifications/initialized) wants no reply
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "helper", "version": "0"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "echo", "description": "echoes text", "inputSchema": map[string]any{"type": "object"}}}}
		case "tools/call":
			var p struct {
				Arguments struct {
					Text string `json:"text"`
				} `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "echo: " + p.Arguments.Text}}}
		default:
			result = map[string]any{}
		}
		_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
	}
}

// Connect spawns a real subprocess, speaks the MCP handshake over its stdio, and
// calls a tool end to end.
func TestConnectStdioRoundTrip(t *testing.T) {
	s := mcp.Server{
		Name:  "helper",
		Scope: mcp.ScopeGlobal,
		Config: mcp.ServerConfig{
			Command: os.Args[0], // re-invoke this test binary as the server (see TestMain)
			Env:     map[string]string{"LLAMINGO_MCP_HELPER": "1"},
		},
	}
	c, err := mcp.Connect(s)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	ctx := context.Background()
	if err := c.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one named 'echo'", tools)
	}
	res, err := c.CallTool(ctx, "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Text(); got != "echo: hi" {
		t.Fatalf("CallTool = %q, want %q", got, "echo: hi")
	}
}

// The legacy two-endpoint "sse" transport is not implemented; Connect says so
// clearly rather than half-dialing it. (http round-trips live in http_test.go.)
func TestConnectUnsupportedTransport(t *testing.T) {
	s := mcp.Server{Name: "remote", Scope: mcp.ScopeGlobal, Config: mcp.ServerConfig{Type: "sse", URL: "https://example.test/sse"}}
	if _, err := mcp.Connect(s); err == nil {
		t.Fatal("Connect to a legacy sse server should error")
	}
}

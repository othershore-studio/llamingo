// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

// startFakeServer runs a minimal MCP server over rw: it answers initialize,
// advertises one "echo" tool, echoes a tools/call to "echo", and errors on an
// unknown tool or method. It stops when the transport closes.
func startFakeServer(t *testing.T, rw io.ReadWriteCloser) {
	t.Helper()
	go func() {
		dec := json.NewDecoder(rw)
		enc := json.NewEncoder(rw)
		fail := func(id int, code int, msg string) {
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id,
				"error": map[string]any{"code": code, "message": msg}})
		}
		ok := func(id int, result any) {
			_ = enc.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
		}
		for {
			var req struct {
				ID     *int            `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if dec.Decode(&req) != nil {
				return // transport closed
			}
			if req.ID == nil {
				continue // notification
			}
			switch req.Method {
			case "initialize":
				ok(*req.ID, map[string]any{
					"protocolVersion": "2025-06-18",
					"serverInfo":      map[string]any{"name": "fake", "version": "0"},
					"capabilities":    map[string]any{},
				})
			case "tools/list":
				ok(*req.ID, map[string]any{"tools": []map[string]any{{
					"name":        "echo",
					"description": "Echo the text argument",
					"inputSchema": map[string]any{"type": "object"},
				}}})
			case "tools/call":
				var p struct {
					Name      string         `json:"name"`
					Arguments map[string]any `json:"arguments"`
				}
				_ = json.Unmarshal(req.Params, &p)
				if p.Name != "echo" {
					fail(*req.ID, -32602, "unknown tool: "+p.Name)
					continue
				}
				ok(*req.ID, map[string]any{"content": []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("echoed: %v", p.Arguments["text"]),
				}}})
			default:
				fail(*req.ID, -32601, "method not found")
			}
		}
	}()
}

func newFakeClient(t *testing.T) *mcp.Client {
	t.Helper()
	client, server := net.Pipe()
	startFakeServer(t, server)
	t.Cleanup(func() { server.Close() })
	c := mcp.NewClient(client)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestClientInitializeListCall(t *testing.T) {
	c := newFakeClient(t)
	ctx := context.Background()

	if err := c.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := c.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v, want one named echo", tools)
	}

	res, err := c.CallTool(ctx, "echo", map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.Text() != "echoed: hi" {
		t.Errorf("result = %q, want %q", res.Text(), "echoed: hi")
	}
}

func TestClientToolErrorSurfaces(t *testing.T) {
	c := newFakeClient(t)
	if _, err := c.CallTool(context.Background(), "does-not-exist", nil); err == nil {
		t.Fatal("want an error for an unknown tool, got nil")
	}
}

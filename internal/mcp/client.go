// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message) }

// Tool is an MCP tool as advertised by tools/list. InputSchema is the raw JSON
// Schema for the tool's arguments, passed through to the model unchanged.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ToolResult is the result of tools/call.
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError"`
}

// Content is one block of a tool result (text only, for now).
type Content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Text concatenates the text blocks of the result.
func (r ToolResult) Text() string {
	var b strings.Builder
	for _, c := range r.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// Client is a minimal JSON-RPC 2.0 client for one MCP server over a
// newline-delimited byte stream — a subprocess's stdio in production, an
// in-memory pipe in tests. Calls are synchronous.
type Client struct {
	mu   sync.Mutex
	rw   io.ReadWriteCloser
	enc  *json.Encoder
	dec  *json.Decoder
	next int
}

// NewClient wraps a transport (e.g. a subprocess's stdio) as an MCP client.
func NewClient(rw io.ReadWriteCloser) *Client {
	return &Client{rw: rw, enc: json.NewEncoder(rw), dec: json.NewDecoder(rw)}
}

// Close closes the underlying transport.
func (c *Client) Close() error { return c.rw.Close() }

// Initialize performs the MCP handshake and signals readiness.
func (c *Client) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "llaminGO", "version": "0"},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return err
	}
	return c.notify("notifications/initialized", nil)
}

// ListTools returns the server's advertised tools.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("mcp: parse tools/list: %w", err)
	}
	return out.Tools, nil
}

// CallTool invokes a tool and returns its result.
func (c *Client) CallTool(ctx context.Context, name string, args any) (ToolResult, error) {
	raw, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return ToolResult{}, err
	}
	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ToolResult{}, fmt.Errorf("mcp: parse tools/call: %w", err)
	}
	return res, nil
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	if err := c.enc.Encode(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, fmt.Errorf("mcp: send %s: %w", method, err)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var resp rpcResponse
		if err := c.dec.Decode(&resp); err != nil {
			return nil, fmt.Errorf("mcp: read %s: %w", method, err)
		}
		if resp.ID == nil || *resp.ID != id {
			continue // skip notifications / unrelated messages
		}
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A JSON-RPC notification is a request with no id.
	if err := c.enc.Encode(map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		return fmt.Errorf("mcp: notify %s: %w", method, err)
	}
	return nil
}

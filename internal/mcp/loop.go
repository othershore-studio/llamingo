// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// Registry is the set of tools the loop can offer the model and execute. The
// concrete ServerRegistry below is backed by MCP servers; tests use a fake.
type Registry interface {
	// Tools returns the tool definitions to offer the model this turn.
	Tools() []backend.ToolDef
	// Call executes a tool by name with raw JSON arguments and returns its text
	// result.
	Call(ctx context.Context, name, arguments string) (string, error)
}

// Run drives the client-owned tool-call loop: it offers the registry's tools to
// the model, executes any tool calls the model makes, feeds the results back,
// and repeats until the model answers without calling tools (or maxRounds is
// reached). It returns the full message list, including the assistant and tool
// turns. onDelta, if non-nil, receives streamed deltas for live display.
func Run(ctx context.Context, be backend.Backend, reg Registry, msgs []backend.Message, onDelta func(backend.Delta) error, maxRounds int) ([]backend.Message, error) {
	if maxRounds <= 0 {
		maxRounds = 8
	}
	for round := 0; round < maxRounds; round++ {
		var content strings.Builder
		var toolCalls []backend.ToolCall

		err := be.ChatStream(ctx, backend.ChatRequest{Messages: msgs, Tools: reg.Tools()}, func(d backend.Delta) error {
			if d.Content != "" {
				content.WriteString(d.Content)
			}
			if onDelta != nil {
				if err := onDelta(d); err != nil {
					return err
				}
			}
			if d.Done {
				toolCalls = d.ToolCalls
			}
			return nil
		})
		if err != nil {
			return msgs, err
		}

		assistant := backend.Message{Role: backend.RoleAssistant, Content: content.String()}
		if len(toolCalls) > 0 {
			assistant.ToolCalls = toolCalls
		}
		msgs = append(msgs, assistant)

		if len(toolCalls) == 0 {
			return msgs, nil // the model answered without calling a tool
		}

		for _, tc := range toolCalls {
			result, err := reg.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				// Surface the failure to the model rather than aborting the loop.
				result = "tool error: " + err.Error()
			}
			msgs = append(msgs, backend.Message{
				Role:       backend.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
	return msgs, fmt.Errorf("mcp: tool loop did not settle within %d rounds", maxRounds)
}

type connected struct {
	server Server
	client *Client
	tools  []Tool
}

// ServerRegistry is a Registry backed by connected MCP servers, honoring trust:
// tools from an unapproved server (a project server, until the increment-#4
// gate approves it) are neither offered nor callable.
type ServerRegistry struct {
	approved func(Server) bool
	conns    []*connected
	byTool   map[string]*connected
}

// NewRegistry returns an empty registry. approved decides which servers' tools
// are live; nil means "global servers only" (the default trust rule).
func NewRegistry(approved func(Server) bool) *ServerRegistry {
	if approved == nil {
		approved = func(s Server) bool { return s.TrustedByDefault() }
	}
	return &ServerRegistry{approved: approved, byTool: map[string]*connected{}}
}

// Add initializes the client, lists its tools, and registers it. The server is
// always recorded, but its tools are offered and callable only if approved.
func (r *ServerRegistry) Add(ctx context.Context, s Server, c *Client) error {
	if err := c.Initialize(ctx); err != nil {
		return fmt.Errorf("mcp: initialize %q: %w", s.Name, err)
	}
	tools, err := c.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("mcp: list tools of %q: %w", s.Name, err)
	}
	cc := &connected{server: s, client: c, tools: tools}
	r.conns = append(r.conns, cc)
	if r.approved(s) {
		for _, t := range tools {
			r.byTool[t.Name] = cc
		}
	}
	return nil
}

// Tools offers the tools of approved servers to the model.
func (r *ServerRegistry) Tools() []backend.ToolDef {
	var out []backend.ToolDef
	for _, cc := range r.conns {
		if !r.approved(cc.server) {
			continue
		}
		for _, t := range cc.tools {
			out = append(out, backend.ToolDef{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	return out
}

// Call routes a tool invocation to the owning server, refusing tools whose
// server is not approved.
func (r *ServerRegistry) Call(ctx context.Context, name, arguments string) (string, error) {
	cc, ok := r.byTool[name]
	if !ok {
		return "", fmt.Errorf("mcp: tool %q is not available (unknown, or its server is not approved)", name)
	}
	var args any
	if strings.TrimSpace(arguments) != "" {
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return "", fmt.Errorf("mcp: tool %q arguments: %w", name, err)
		}
	}
	res, err := cc.client.CallTool(ctx, name, args)
	if err != nil {
		return "", err
	}
	if res.IsError {
		return "", fmt.Errorf("mcp: tool %q reported an error: %s", name, res.Text())
	}
	return res.Text(), nil
}

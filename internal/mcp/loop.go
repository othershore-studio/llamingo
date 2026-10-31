// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/debuglog"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// Registry is the set of tools the loop can offer the model and execute. The
// ServerRegistry below is backed by MCP servers; a native tool (e.g. image
// generation) satisfies this interface too, and Combine merges them.
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
// templateKwargs, when non-nil, pass per-request variables to the model's chat
// template on every round (e.g. {"enable_thinking": false}).
func Run(ctx context.Context, be backend.Backend, reg Registry, msgs []backend.Message, onDelta func(backend.Delta) error, maxRounds int, templateKwargs map[string]any) ([]backend.Message, error) {
	if maxRounds <= 0 {
		maxRounds = 8
	}
	for round := 0; round < maxRounds; round++ {
		var content strings.Builder
		var toolCalls []backend.ToolCall

		tools := reg.Tools()
		// On the round right after a tool produced a user-facing image, steer the
		// reply toward a clean caption (see withShapingGuidance). The guidance is
		// transient — sent this round, not appended to msgs — so it never persists
		// in the conversation or stacks across rounds.
		reqMsgs := withShapingGuidance(msgs)
		llamaID := debuglog.NewID()
		debuglog.Record(ctx, debuglog.ChannelLlama, debuglog.KindRequest, llamaID, map[string]any{"messages": reqMsgs, "tools": tools, "templateKwargs": templateKwargs})
		err := be.ChatStream(ctx, backend.ChatRequest{Messages: reqMsgs, Tools: tools, TemplateKwargs: templateKwargs}, func(d backend.Delta) error {
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
		debuglog.Record(ctx, debuglog.ChannelLlama, debuglog.KindResponse, llamaID, map[string]any{"content": assistant.Content, "toolCalls": toolCalls})

		if len(toolCalls) == 0 {
			return msgs, nil // the model answered without calling a tool
		}

		for _, tc := range toolCalls {
			toolID := debuglog.NewID()
			debuglog.Record(ctx, debuglog.ChannelTool, debuglog.KindRequest, toolID, map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments})
			result, err := reg.Call(ctx, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				// Surface the failure to the model rather than aborting the loop.
				result = "tool error: " + err.Error()
			}
			debuglog.Record(ctx, debuglog.ChannelTool, debuglog.KindResponse, toolID, map[string]any{"name": tc.Function.Name, "result": result})
			msgs = append(msgs, backend.Message{
				Role:       backend.RoleTool,
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}
	return msgs, fmt.Errorf("mcp: tool loop did not settle within %d rounds", maxRounds)
}

// shapingGuidance steers the model's reply on the round right after a tool has
// produced a user-facing image. Small models (notably Gemma under a large
// toolset) tend to re-narrate the tool call as a ReAct JSON blob and repeat the
// link; this asks for a clean caption instead. The image is attached
// deterministically from the tool result afterward (see httpapi), so the model
// is told not to repeat it: the model writes the words, the client shows the
// picture.
const shapingGuidance = "The image has been generated and is shown to the user automatically. " +
	"Reply with a short, friendly caption of one or two sentences. " +
	"Do not output any JSON, tool-call syntax, or a markdown image link."

// withShapingGuidance returns the messages to send for a round, appending a
// transient system message with shapingGuidance when the previous turn is a tool
// result carrying an image (markdown "![…]"). It leaves msgs untouched after an
// intermediate tool whose text has no image (e.g. list_image_models) so a
// legitimate follow-up tool call is not discouraged, and never mutates msgs.
func withShapingGuidance(msgs []backend.Message) []backend.Message {
	n := len(msgs)
	if n == 0 || msgs[n-1].Role != backend.RoleTool || !strings.Contains(msgs[n-1].Content, "![") {
		return msgs
	}
	return append(append(make([]backend.Message, 0, n+1), msgs...), backend.Message{Role: backend.RoleSystem, Content: shapingGuidance})
}

type connected struct {
	server Server
	client *Client
	tools  []Tool
}

// ServerRegistry is a Registry backed by connected MCP servers, with the
// permission gate mediating project-server tools. A trusted (global) server
// runs freely; a project server's tools are offered to the model but must clear
// the gate on first use — persistent/session grants remember the answer.
type ServerRegistry struct {
	gate   *permission.Gate
	conns  []*connected
	byTool map[string]*connected
}

// NewRegistry returns an empty registry whose project-server tools are mediated
// by gate.
func NewRegistry(gate *permission.Gate) *ServerRegistry {
	return &ServerRegistry{gate: gate, byTool: map[string]*connected{}}
}

// Add initializes the client, lists its tools, and registers them. All tools
// are offered; trust is enforced at call time by the gate.
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
	for _, t := range tools {
		r.byTool[t.Name] = cc
	}
	return nil
}

// Tools offers every connected server's tools to the model.
func (r *ServerRegistry) Tools() []backend.ToolDef {
	var out []backend.ToolDef
	for _, cc := range r.conns {
		for _, t := range cc.tools {
			out = append(out, toToolDef(t))
		}
	}
	return out
}

// ServerTools is one MCP server's advertised tools, for grouping them by server in
// the curation UI (Combine flattens them for the model; the UI wants groups).
type ServerTools struct {
	Server string
	Tools  []backend.ToolDef
}

// ToolsByServer returns each connected server's tools, in connection order.
func (r *ServerRegistry) ToolsByServer() []ServerTools {
	out := make([]ServerTools, 0, len(r.conns))
	for _, cc := range r.conns {
		g := ServerTools{Server: cc.server.Name}
		for _, t := range cc.tools {
			g.Tools = append(g.Tools, toToolDef(t))
		}
		out = append(out, g)
	}
	return out
}

// Call routes a tool invocation to its server. A trusted (global) server runs
// freely; a project server must clear the gate first.
func (r *ServerRegistry) Call(ctx context.Context, name, arguments string) (string, error) {
	cc, ok := r.byTool[name]
	if !ok {
		return "", fmt.Errorf("mcp: unknown tool %q", name)
	}
	if !cc.server.TrustedByDefault() {
		t, _ := findTool(cc.tools, name)
		allow, err := r.gate.Allow(ctx, permission.Request{
			ToolID: mcpToolID(cc.server, t),
			Label:  fmt.Sprintf("run tool %q from project server %q", name, cc.server.Name),
			Detail: arguments,
			Risk:   permission.RiskMedium,
		})
		if err != nil {
			return "", err
		}
		if !allow {
			return "", fmt.Errorf("mcp: tool %q from project server %q is not permitted", name, cc.server.Name)
		}
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

func toToolDef(t Tool) backend.ToolDef {
	return backend.ToolDef{Name: t.Name, Description: t.Description, Parameters: t.InputSchema}
}

func findTool(tools []Tool, name string) (Tool, bool) {
	for _, t := range tools {
		if t.Name == name {
			return t, true
		}
	}
	return Tool{}, false
}

// mcpToolID is the gate identity of an MCP tool: server + name + a hash of the
// tool definition, so a reshaped tool re-prompts (the confused-deputy guard).
func mcpToolID(s Server, t Tool) string {
	sum := sha256.Sum256([]byte(t.Name + "\x00" + t.Description + "\x00" + string(t.InputSchema)))
	return fmt.Sprintf("mcp:%s:%s#%s", s.Name, t.Name, hex.EncodeToString(sum[:])[:8])
}

// Combine merges registries into one for the loop: Tools concatenates them, and
// Call routes to the first registry that offers the named tool. This is how MCP
// tools and native tools (e.g. image generation) are offered together.
func Combine(regs ...Registry) Registry { return combined(regs) }

type combined []Registry

func (c combined) Tools() []backend.ToolDef {
	var out []backend.ToolDef
	for _, r := range c {
		out = append(out, r.Tools()...)
	}
	return out
}

func (c combined) Call(ctx context.Context, name, arguments string) (string, error) {
	for _, r := range c {
		for _, t := range r.Tools() {
			if t.Name == name {
				return r.Call(ctx, name, arguments)
			}
		}
	}
	return "", fmt.Errorf("mcp: no registry offers tool %q", name)
}

var (
	_ Registry = (*ServerRegistry)(nil)
	_ Registry = combined(nil)
)

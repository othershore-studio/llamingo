// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// echoReg is a fake Registry: one "echo" tool whose Call always succeeds.
type echoReg struct{ calls int }

func (r *echoReg) Tools() []backend.ToolDef {
	return []backend.ToolDef{{Name: "echo", Description: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}
}

func (r *echoReg) Call(ctx context.Context, name, arguments string) (string, error) {
	r.calls++
	return "echoed: hi", nil
}

// staticReg offers one named tool returning a fixed result.
type staticReg struct{ name, result string }

func (s *staticReg) Tools() []backend.ToolDef {
	return []backend.ToolDef{{Name: s.name, Parameters: json.RawMessage(`{"type":"object"}`)}}
}

func (s *staticReg) Call(ctx context.Context, name, arguments string) (string, error) {
	if name != s.name {
		return "", fmt.Errorf("no such tool %q", name)
	}
	return s.result, nil
}

type allowPrompter struct{}

func (allowPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: true, Scope: permission.ScopeSession}, nil
}

type denyPrompter struct{}

func (denyPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: false}, nil
}

func gate(p permission.Prompter) *permission.Gate {
	return permission.New(p, permission.NewMemoryGrantStore())
}

const toolTurnSSE = `data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}

data: [DONE]
`

const textTurnSSE = `data: {"choices":[{"index":0,"delta":{"content":"The echo said hi."}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":5,"total_tokens":45}}

data: [DONE]
`

func TestRunLoop(t *testing.T) {
	srv := lcpptest.NewServerScript(toolTurnSSE, textTurnSSE)
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	reg := &echoReg{}

	start := []backend.Message{{Role: backend.RoleUser, Content: "say hi via echo"}}
	msgs, err := mcp.Run(context.Background(), be, reg, start, nil, 8)
	if err != nil {
		t.Fatal(err)
	}

	if reg.calls != 1 {
		t.Errorf("tool calls = %d, want 1", reg.calls)
	}
	last := msgs[len(msgs)-1]
	if last.Role != backend.RoleAssistant || last.Content != "The echo said hi." {
		t.Errorf("final message = %+v, want assistant answer", last)
	}

	var sawToolCall, sawToolResult bool
	for _, m := range msgs {
		if m.Role == backend.RoleAssistant && len(m.ToolCalls) == 1 && m.ToolCalls[0].Function.Name == "echo" {
			sawToolCall = true
		}
		if m.Role == backend.RoleTool && m.Content == "echoed: hi" && m.ToolCallID == "call_1" {
			sawToolResult = true
		}
	}
	if !sawToolCall {
		t.Error("no assistant message carrying the tool call")
	}
	if !sawToolResult {
		t.Error("no tool-result message fed back to the model")
	}
}

func TestServerRegistryGating(t *testing.T) {
	ctx := context.Background()

	// A global (trusted) server runs freely — the gate is never consulted, so a
	// denying prompter still lets it through.
	trusted := mcp.NewRegistry(gate(denyPrompter{}))
	if err := trusted.Add(ctx, mcp.Server{Name: "g", Scope: mcp.ScopeGlobal}, newFakeClient(t)); err != nil {
		t.Fatal(err)
	}
	if tools := trusted.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("trusted tools = %+v", tools)
	}
	if out, err := trusted.Call(ctx, "echo", `{"text":"hi"}`); err != nil || out != "echoed: hi" {
		t.Fatalf("trusted call = %q, %v (should bypass the gate)", out, err)
	}

	// A project (untrusted) server offers its tools and runs them when the gate
	// allows.
	project := mcp.NewRegistry(gate(allowPrompter{}))
	if err := project.Add(ctx, mcp.Server{Name: "p", Scope: mcp.ScopeProject}, newFakeClient(t)); err != nil {
		t.Fatal(err)
	}
	if tools := project.Tools(); len(tools) != 1 {
		t.Fatalf("project tools should still be offered, got %+v", tools)
	}
	if out, err := project.Call(ctx, "echo", `{"text":"hi"}`); err != nil || out != "echoed: hi" {
		t.Fatalf("project call (gate allows) = %q, %v", out, err)
	}

	// With a denying gate, the project server's tool is refused.
	denied := mcp.NewRegistry(gate(denyPrompter{}))
	if err := denied.Add(ctx, mcp.Server{Name: "p2", Scope: mcp.ScopeProject}, newFakeClient(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := denied.Call(ctx, "echo", `{"text":"hi"}`); err == nil {
		t.Error("want a refusal when the gate denies a project tool")
	}
}

func TestCombine(t *testing.T) {
	combo := mcp.Combine(&echoReg{}, &staticReg{name: "ping", result: "pong"})

	names := map[string]bool{}
	for _, d := range combo.Tools() {
		names[d.Name] = true
	}
	if !names["echo"] || !names["ping"] {
		t.Fatalf("combined tools = %v, want echo and ping", names)
	}

	if out, err := combo.Call(context.Background(), "ping", ""); err != nil || out != "pong" {
		t.Errorf("Call(ping) = %q, %v", out, err)
	}
	if _, err := combo.Call(context.Background(), "nope", ""); err == nil {
		t.Error("want an error for a tool no registry offers")
	}
}

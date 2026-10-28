// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/mcp"
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

func TestServerRegistryTrust(t *testing.T) {
	ctx := context.Background()

	// A global (trusted) server: its tools are offered and callable.
	trusted := mcp.NewRegistry(nil)
	if err := trusted.Add(ctx, mcp.Server{Name: "g", Scope: mcp.ScopeGlobal}, newFakeClient(t)); err != nil {
		t.Fatal(err)
	}
	if tools := trusted.Tools(); len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("trusted tools = %+v, want one named echo", tools)
	}
	out, err := trusted.Call(ctx, "echo", `{"text":"hi"}`)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if out != "echoed: hi" {
		t.Errorf("Call result = %q", out)
	}

	// A project (untrusted) server: tools withheld and calls refused until the
	// gate approves it (increment #4).
	untrusted := mcp.NewRegistry(nil)
	if err := untrusted.Add(ctx, mcp.Server{Name: "p", Scope: mcp.ScopeProject}, newFakeClient(t)); err != nil {
		t.Fatal(err)
	}
	if tools := untrusted.Tools(); len(tools) != 0 {
		t.Errorf("untrusted server offered tools: %+v", tools)
	}
	if _, err := untrusted.Call(ctx, "echo", `{"text":"hi"}`); err == nil {
		t.Error("want a refusal for an unapproved server's tool, got nil")
	}
}

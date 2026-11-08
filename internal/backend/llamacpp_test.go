// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
)

func newClient(t *testing.T) *backend.LlamaCppNative {
	t.Helper()
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	return backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
}

func TestProps(t *testing.T) {
	p, err := newClient(t).Props(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if p.NCtx != lcpptest.NCtx {
		t.Errorf("NCtx = %d, want %d", p.NCtx, lcpptest.NCtx)
	}
	if !strings.Contains(p.ModelID, "gemma") {
		t.Errorf("ModelID = %q, want it to mention gemma", p.ModelID)
	}
	if p.ChatTemplate == "" {
		t.Error("ChatTemplate is empty")
	}
}

func TestTokenize(t *testing.T) {
	text := "the quick brown fox jumps over the lazy dog"
	n, err := newClient(t).Tokenize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if want := lcpptest.ApproxTokens(text); n != want {
		t.Errorf("Tokenize = %d, want %d", n, want)
	}
}

func TestApplyTemplate(t *testing.T) {
	msgs := []backend.Message{{Role: backend.RoleUser, Content: "Hello"}}
	prompt, err := newClient(t).ApplyTemplate(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Hello") || !strings.Contains(prompt, "<start_of_turn>model") {
		t.Errorf("prompt = %q, missing content or generation prompt", prompt)
	}
}

func TestChatStream(t *testing.T) {
	var content, reasoning strings.Builder
	var usage *backend.Usage
	sawDone := false

	err := newClient(t).ChatStream(context.Background(),
		backend.ChatRequest{Messages: []backend.Message{{Role: backend.RoleUser, Content: "Hi"}}},
		func(d backend.Delta) error {
			content.WriteString(d.Content)
			reasoning.WriteString(d.Reasoning)
			if d.Usage != nil {
				usage = d.Usage
			}
			if d.Done {
				sawDone = true
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if got := content.String(); got != "Hello! How can I help you today?" {
		t.Errorf("content = %q", got)
	}
	if got := reasoning.String(); got != "The user greeted me. I'll respond warmly and offer help." {
		t.Errorf("reasoning = %q", got)
	}
	if !sawDone {
		t.Error("never saw a terminal (Done) delta")
	}
	if usage == nil || usage.PromptTokens != 12 {
		t.Errorf("usage = %+v, want PromptTokens 12", usage)
	}
}

// A backend that streams a little, then goes silent (an OOM-killed or crashed
// llama-server) must not hang the turn forever: the idle-timeout aborts the read
// and returns a clear error, after delivering whatever streamed first.
func TestChatStreamIdleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Hi\"}}]}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done() // go silent, holding the connection open
	}))
	defer srv.Close()

	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()), backend.WithStreamIdleTimeout(200*time.Millisecond))
	var content strings.Builder
	err := be.ChatStream(context.Background(),
		backend.ChatRequest{Messages: []backend.Message{{Role: backend.RoleUser, Content: "hi"}}},
		func(d backend.Delta) error {
			content.WriteString(d.Content)
			return nil
		})
	if err == nil {
		t.Fatal("expected an idle-timeout error from a silent backend, got nil")
	}
	if !strings.Contains(err.Error(), "silent") {
		t.Errorf("error = %v, want a 'went silent' timeout error", err)
	}
	if content.String() != "Hi" {
		t.Errorf("content before the stall = %q, want the partial %q", content.String(), "Hi")
	}
}

// A model "lost in thought" never stops generating: it streams reasoning (or
// tokens) continuously, so the idle-timeout — which resets on every line — can
// never fire. The lost-in-thought watchdog bounds the total generation time and
// aborts with a clear error, after delivering whatever streamed first. A damaged
// or incompletely-downloaded model can fail this way, not only an abliterated one.
func TestChatStreamLostInThought(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f, _ := w.(http.Flusher)
		// Stream reasoning forever (never a [DONE] or finish), keeping the stream
		// live so only the lost-in-thought cap — not the idle-timeout — can stop it.
		for {
			if _, err := io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"and another thought \"}}]}\n\n"); err != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	}))
	defer srv.Close()

	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()), backend.WithLostInThoughtTimeout(200*time.Millisecond))
	var reasoning strings.Builder
	err := be.ChatStream(context.Background(),
		backend.ChatRequest{Messages: []backend.Message{{Role: backend.RoleUser, Content: "hi"}}},
		func(d backend.Delta) error {
			reasoning.WriteString(d.Reasoning)
			return nil
		})
	if err == nil {
		t.Fatal("expected a lost-in-thought error from a never-ending stream, got nil")
	}
	if !strings.Contains(err.Error(), "lost in thought") {
		t.Errorf("error = %v, want a 'lost in thought' error", err)
	}
	if reasoning.Len() == 0 {
		t.Error("expected the partial reasoning that streamed before the cap to be delivered")
	}
}

func TestChatStreamToolCalls(t *testing.T) {
	// Tool-call arguments arrive as two fragments and must be accumulated.
	const toolSSE = `data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":"}}]}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"hi\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":25}}

data: [DONE]
`
	srv := lcpptest.NewServerScript(toolSSE)
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))

	var got []backend.ToolCall
	sawDone := false
	err := be.ChatStream(context.Background(),
		backend.ChatRequest{
			Messages: []backend.Message{{Role: backend.RoleUser, Content: "say hi via echo"}},
			Tools:    []backend.ToolDef{{Name: "echo", Description: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}},
		},
		func(d backend.Delta) error {
			if d.Done {
				sawDone = true
				got = d.ToolCalls
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !sawDone {
		t.Fatal("never saw a terminal (Done) delta")
	}
	if len(got) != 1 {
		t.Fatalf("tool calls = %+v, want exactly 1", got)
	}
	tc := got[0]
	if tc.ID != "call_1" || tc.Function.Name != "echo" || tc.Function.Arguments != `{"text":"hi"}` {
		t.Errorf("tool call = %+v, want id call_1, name echo, args {\"text\":\"hi\"}", tc)
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package lcpptest provides a deterministic, in-process fake of a llama.cpp
// `llama-server`, so the engine path is tested GPU-free. /props and the
// streamed completion(s) are replayed fixtures; /tokenize and /apply-template
// are computed deterministically so context-budget math is assertable. Nothing
// in the engine path merges without a test through here.
package lcpptest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
)

//go:embed testdata/props.json
var propsJSON []byte

//go:embed testdata/chat-stream.txt
var chatStreamSSE []byte

// NCtx is the context window the fake /props reports (kept in sync with
// testdata/props.json).
const NCtx = 8192

// ApproxTokens is the fake tokenizer: a deterministic ~4-characters-per-token
// estimate. Tests use it to predict the counts the fake will return.
func ApproxTokens(s string) int {
	n := len([]rune(s))
	if n == 0 {
		return 0
	}
	return (n + 3) / 4
}

// NewServer starts a fake llama-server whose chat endpoint always replies with
// the default text completion.
func NewServer() *httptest.Server { return NewServerScript(string(chatStreamSSE)) }

// NewServerScript starts a fake llama-server whose /v1/chat/completions returns
// the given SSE bodies on successive calls (the last one repeats). This lets a
// test script a multi-round exchange — e.g. a tool call, then a final answer.
func NewServerScript(chatTurns ...string) *httptest.Server {
	if len(chatTurns) == 0 {
		chatTurns = []string{string(chatStreamSSE)}
	}
	var calls int32

	mux := http.NewServeMux()

	mux.HandleFunc("/props", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(propsJSON)
	})

	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		toks := make([]int, ApproxTokens(req.Content))
		for i := range toks {
			toks[i] = i
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tokens": toks})
	})

	mux.HandleFunc("/apply-template", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var b strings.Builder
		for _, m := range req.Messages {
			fmt.Fprintf(&b, "<start_of_turn>%s\n%s<end_of_turn>\n", m.Role, m.Content)
		}
		b.WriteString("<start_of_turn>model\n")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"prompt": b.String()})
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		i := int(atomic.AddInt32(&calls, 1)) - 1
		if i >= len(chatTurns) {
			i = len(chatTurns) - 1
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write([]byte(chatTurns[i]))
	})

	return httptest.NewServer(mux)
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package lcpptest provides a deterministic, in-process fake of a llama.cpp
// `llama-server`, so the engine path is tested GPU-free. /props and the
// streamed completion are replayed fixtures; /tokenize and /apply-template are
// computed deterministically so context-budget math is assertable. Nothing in
// the engine path merges without a test through here.
package lcpptest

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

// NewServer starts a fake llama-server and returns it. The caller must Close it.
func NewServer() *httptest.Server {
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
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(chatStreamSSE)
	})

	return httptest.NewServer(mux)
}

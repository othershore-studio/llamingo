// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

// Round 1: the model calls write_memory. Round 2 (after the tool result is fed
// back): it answers in prose.
const writeMemoryToolSSE = `data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}

data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_mem","type":"function","function":{"name":"write_memory","arguments":"{\"content\":\"Daniel prefers tea.\"}"}}]}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: {"choices":[],"usage":{"prompt_tokens":30,"completion_tokens":8,"total_tokens":38}}

data: [DONE]
`

const savedTextSSE = `data: {"choices":[{"index":0,"delta":{"content":"Saved that to your memory."}}]}

data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":50,"completion_tokens":6,"total_tokens":56}}

data: [DONE]
`

// The full agentic path end to end: the model calls write_memory, which pauses
// on a permission prompt sent over the SSE stream; the client answers it on a
// separate POST; the tool runs, the loop resumes, and the memory is written.
func TestAgenticWriteMemoryHandshake(t *testing.T) {
	llama := lcpptest.NewServerScript(writeMemoryToolSSE, savedTextSSE)
	t.Cleanup(llama.Close)
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(be, st))
	t.Cleanup(api.Close)

	id := createChat(t, api, "Tools")

	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"Remember that I prefer tea."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Read the SSE stream incrementally: when the permission prompt arrives,
	// answer it (allow, this session) so the paused tool call resumes.
	reader := bufio.NewReader(resp.Body)
	var ev, data string
	var permID string
	var sawToolCall, sawDone bool
	for {
		line, rerr := reader.ReadString('\n')
		switch trimmed := strings.TrimRight(line, "\r\n"); {
		case strings.HasPrefix(trimmed, "event: "):
			ev = strings.TrimPrefix(trimmed, "event: ")
		case strings.HasPrefix(trimmed, "data: "):
			data = strings.TrimPrefix(trimmed, "data: ")
		case trimmed == "" && ev != "": // blank line ends an event
			switch ev {
			case "permission":
				var p struct {
					ID     string   `json:"id"`
					Risk   string   `json:"risk"`
					Detail string   `json:"detail"`
					Scopes []string `json:"scopes"`
				}
				if err := json.Unmarshal([]byte(data), &p); err != nil {
					t.Fatalf("permission payload: %v", err)
				}
				permID = p.ID
				if p.Risk != "medium" {
					t.Errorf("risk = %q, want medium", p.Risk)
				}
				if !strings.Contains(p.Detail, "tea") {
					t.Errorf("detail = %q, want the proposed content shown for consent", p.Detail)
				}
				if len(p.Scopes) == 0 {
					t.Error("no grant scopes offered")
				}
				ans, aerr := http.Post(api.URL+"/api/permission/"+permID, "application/json",
					strings.NewReader(`{"allow":true,"scope":"session"}`))
				if aerr != nil {
					t.Fatal(aerr)
				}
				if ans.StatusCode != http.StatusNoContent {
					t.Errorf("permission answer status = %d, want 204", ans.StatusCode)
				}
				ans.Body.Close()
			case "tool_call":
				sawToolCall = true
			case "done":
				sawDone = true
			}
			ev, data = "", ""
		}
		if rerr != nil {
			break
		}
	}

	if permID == "" {
		t.Fatal("no permission prompt was emitted — the gate did not reach the stream")
	}
	if !sawToolCall {
		t.Error("no tool_call event (the tool activity was not surfaced)")
	}
	if !sawDone {
		t.Error("stream did not finish with a done event")
	}

	// The memory was actually written by the tool.
	mresp, err := http.Get(api.URL + "/api/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(mresp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m["content"] != "Daniel prefers tea." {
		t.Errorf("memory = %q, want the content the tool wrote", m["content"])
	}

	// Only the final assistant answer is persisted — not the tool scratch.
	entries := getConversation(t, api, id)
	last := entries[len(entries)-1]
	reply, _ := last["content"].(string)
	if last["role"] != "assistant" || !strings.Contains(reply, "Saved") {
		t.Errorf("final entry = %+v, want the assistant's prose answer", last)
	}
	for _, e := range entries {
		if c, _ := e["content"].(string); strings.Contains(c, `"content":"Daniel`) {
			t.Errorf("tool-call scratch leaked into the log: %+v", e)
		}
	}
}

// When the user denies the prompt, the tool does not run and the model is told.
func TestAgenticWriteMemoryDenied(t *testing.T) {
	llama := lcpptest.NewServerScript(writeMemoryToolSSE, savedTextSSE)
	t.Cleanup(llama.Close)
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(be, st))
	t.Cleanup(api.Close)

	id := createChat(t, api, "Tools")
	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"Remember that I prefer tea."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var ev, data string
	for {
		line, rerr := reader.ReadString('\n')
		switch trimmed := strings.TrimRight(line, "\r\n"); {
		case strings.HasPrefix(trimmed, "event: "):
			ev = strings.TrimPrefix(trimmed, "event: ")
		case strings.HasPrefix(trimmed, "data: "):
			data = strings.TrimPrefix(trimmed, "data: ")
		case trimmed == "" && ev != "":
			if ev == "permission" {
				var p struct {
					ID string `json:"id"`
				}
				json.Unmarshal([]byte(data), &p)
				ans, _ := http.Post(api.URL+"/api/permission/"+p.ID, "application/json",
					strings.NewReader(`{"allow":false}`))
				ans.Body.Close()
			}
			ev, data = "", ""
		}
		if rerr != nil {
			break
		}
	}

	// Memory stays empty — the denied tool never wrote.
	mresp, err := http.Get(api.URL + "/api/memory")
	if err != nil {
		t.Fatal(err)
	}
	defer mresp.Body.Close()
	var m map[string]any
	json.NewDecoder(mresp.Body).Decode(&m)
	if m["content"] != "" {
		t.Errorf("memory = %q, want empty after a denied write", m["content"])
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

func newAPI(t *testing.T) *httptest.Server {
	t.Helper()
	llama := lcpptest.NewServer()
	t.Cleanup(llama.Close)
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(be, st))
	t.Cleanup(api.Close)
	return api
}

// createChat posts a new free chat and returns its id.
func createChat(t *testing.T, api *httptest.Server, title string) string {
	t.Helper()
	resp, err := http.Post(api.URL+"/api/chats", "application/json", strings.NewReader(`{"title":"`+title+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create chat status = %d", resp.StatusCode)
	}
	var m struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	if m.ID == "" {
		t.Fatal("created chat has no id")
	}
	return m.ID
}

func getConversation(t *testing.T, api *httptest.Server, id string) []map[string]any {
	t.Helper()
	resp, err := http.Get(api.URL + "/api/conversations/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get conversation status = %d", resp.StatusCode)
	}
	var entries []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

// postJSON posts a JSON body and returns the decoded object.
func postJSON(t *testing.T, api *httptest.Server, path, body string) map[string]any {
	t.Helper()
	resp, err := http.Post(api.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", path, resp.StatusCode)
	}
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

// getList gets a JSON array and returns it.
func getList(t *testing.T, api *httptest.Server, path string) []map[string]any {
	t.Helper()
	resp, err := http.Get(api.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d", path, resp.StatusCode)
	}
	var list []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	return list
}

// Empty lists serialize as [] (a nil Go slice would be null, which a client
// iterating or reading .length chokes on).
func TestEmptyListsAreArrays(t *testing.T) {
	api := newAPI(t)
	for _, path := range []string{"/api/chats", "/api/projects"} {
		resp, err := http.Get(api.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.TrimSpace(string(b)) != "[]" {
			t.Errorf("%s empty body = %q, want []", path, b)
		}
	}
}

func TestMemoryEndpoints(t *testing.T) {
	api := newAPI(t)

	get := func() map[string]any {
		resp, err := http.Get(api.URL + "/api/memory")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if m := get(); m["content"] != "" || m["tampered"] != false {
		t.Fatalf("empty memory = %+v, want blank and not tampered", m)
	}

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/memory", strings.NewReader(`{"content":"likes tea"}`))
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT /api/memory status = %d, want 204", resp.StatusCode)
	}

	if m := get(); m["content"] != "likes tea" || m["tampered"] != false {
		t.Fatalf("after PUT = %+v, want the saved content and not tampered", m)
	}
}

func TestProps(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Get(api.URL + "/api/props")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var p struct {
		Model             string `json:"model"`
		NCtx              int    `json:"nCtx"`
		ThinkingSupported bool   `json:"thinkingSupported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.NCtx != lcpptest.NCtx {
		t.Errorf("nCtx = %d, want %d", p.NCtx, lcpptest.NCtx)
	}
	if !strings.Contains(p.Model, "gemma") {
		t.Errorf("model = %q", p.Model)
	}
	if !p.ThinkingSupported {
		t.Error("thinkingSupported = false, want true (the fixture template has enable_thinking)")
	}
}

// With the backend unreachable, /api/props fails with 502 — the signal the UI's
// server monitor polls on to show a disconnected cue.
func TestPropsBackendDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := dead.URL
	dead.Close() // the backend now refuses connections

	be := backend.New(url)
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(be, st))
	t.Cleanup(api.Close)

	resp, err := http.Get(api.URL + "/api/props")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("props with a down backend = %d, want 502", resp.StatusCode)
	}
}

func TestChatsCreateAndList(t *testing.T) {
	api := newAPI(t)
	createChat(t, api, "Trip ideas")

	resp, err := http.Get(api.URL + "/api/chats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var chats []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&chats); err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0]["title"] != "Trip ideas" {
		t.Fatalf("chats = %+v, want one titled 'Trip ideas'", chats)
	}
}

// Projects: open a folder as a project, create a session in it, and confirm a
// session is a full conversation (a streamed turn persists to its log).
func TestProjectsAndSessions(t *testing.T) {
	api := newAPI(t)

	proj := postJSON(t, api, "/api/projects", `{"path":"/projects/acme","title":"Acme"}`)
	pid, _ := proj["id"].(string)
	if pid == "" || proj["title"] != "Acme" {
		t.Fatalf("project = %+v, want an id and title Acme", proj)
	}

	// Re-opening the same path is idempotent (identity is the path hash).
	if again := postJSON(t, api, "/api/projects", `{"path":"/projects/acme","title":"Acme"}`); again["id"] != pid {
		t.Errorf("re-open gave a different id: %v vs %v", again["id"], pid)
	}

	if projects := getList(t, api, "/api/projects"); len(projects) != 1 {
		t.Fatalf("projects = %+v, want exactly 1", projects)
	}

	if sessions := getList(t, api, "/api/projects/"+pid+"/sessions"); len(sessions) != 0 {
		t.Fatalf("new project has %d sessions, want 0", len(sessions))
	}

	sess := postJSON(t, api, "/api/projects/"+pid+"/sessions", `{"title":"First session"}`)
	sid, _ := sess["id"].(string)
	if sid == "" {
		t.Fatal("session has no id")
	}

	sessions := getList(t, api, "/api/projects/"+pid+"/sessions")
	if len(sessions) != 1 || sessions[0]["title"] != "First session" {
		t.Fatalf("sessions = %+v, want one titled 'First session'", sessions)
	}

	// A session is a full conversation: a streamed turn persists to its log.
	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+sid+`","content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	sessEntries := getConversation(t, api, sid)
	sessMsgs := 0
	for _, e := range sessEntries {
		if e["kind"] == "msg" {
			sessMsgs++
		}
	}
	if sessMsgs != 2 {
		t.Fatalf("session conversation has %d message entries, want 2 (user + assistant)", sessMsgs)
	}
}

// Compaction folds the oldest messages into a summary, non-destructively: the
// originals stay in the log and a summary entry is appended.
func TestCompact(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Long chat")

	// A short conversation has nothing to fold yet.
	if r := postJSON(t, api, "/api/compact", `{"id":"`+id+`"}`); int(r["folded"].(float64)) != 0 {
		t.Fatalf("compact on short chat folded %v, want 0", r["folded"])
	}

	// Build several turns (each persists a user + assistant message).
	for i := 0; i < 3; i++ {
		resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
			strings.NewReader(`{"id":"`+id+`","content":"turn"}`))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	r := postJSON(t, api, "/api/compact", `{"id":"`+id+`"}`)
	if folded := int(r["folded"].(float64)); folded <= 0 {
		t.Fatalf("compact folded %d, want > 0", folded)
	}

	// Append-only: all six messages remain, with one summary entry added.
	entries := getConversation(t, api, id)
	msgs, summaries := 0, 0
	for _, e := range entries {
		switch e["kind"] {
		case "msg":
			msgs++
		case "summary":
			summaries++
		}
	}
	if msgs != 6 {
		t.Errorf("got %d msg entries, want 6 (nothing deleted)", msgs)
	}
	if summaries != 1 {
		t.Errorf("got %d summary entries, want 1", summaries)
	}
}

// A new conversation has no entries yet — GET returns an empty list, not an error.
func TestGetNewConversationEmpty(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Empty")
	if entries := getConversation(t, api, id); len(entries) != 0 {
		t.Fatalf("new conversation has %d entries, want 0", len(entries))
	}
}

// Streaming a turn: the reply streams as SSE, and both the user message and the
// assistant reply are persisted to the conversation's log.
func TestChatStreamPersists(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Chat")

	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"Hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q, want text/event-stream", ct)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	body := buf.String()

	if !strings.Contains(body, "event: delta") || !strings.Contains(body, "Hello") || !strings.Contains(body, "How can I help you today") {
		t.Errorf("missing streamed content:\n%s", body)
	}
	if !strings.Contains(body, "event: done") || !strings.Contains(body, `"used"`) || !strings.Contains(body, `"level"`) {
		t.Errorf("missing done event with budget:\n%s", body)
	}

	entries := getConversation(t, api, id)
	// The turn persists the user message, the thinking trace, and the reply.
	var msgs, reasoning int
	var userE, asstE map[string]any
	for _, e := range entries {
		switch e["kind"] {
		case "msg":
			msgs++
			if e["role"] == "user" {
				userE = e
			}
			if e["role"] == "assistant" {
				asstE = e
			}
		case "reasoning":
			reasoning++
		}
	}
	if msgs != 2 {
		t.Fatalf("persisted %d message entries, want 2 (user + assistant): %+v", msgs, entries)
	}
	if reasoning != 1 {
		t.Errorf("persisted %d reasoning entries, want 1 (the thinking trace)", reasoning)
	}
	if userE == nil || userE["content"] != "Hi" {
		t.Errorf("user entry = %+v, want the user's 'Hi'", userE)
	}
	reply, _ := asstE["content"].(string)
	if asstE == nil || !strings.Contains(reply, "Hello") {
		t.Errorf("assistant entry = %+v, want the assistant reply", asstE)
	}
}

// Reasoning ("thinking") streams as its own SSE events and is persisted as a
// reasoning entry — shown on reload, but kept out of the prompt (Project drops
// it) and never mixed into the assistant's reply.
func TestChatStreamReasoning(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Thinker")

	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"Hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	body := buf.String()

	if !strings.Contains(body, "event: reasoning") || !strings.Contains(body, "respond warmly") {
		t.Errorf("missing reasoning stream:\n%s", body)
	}

	entries := getConversation(t, api, id)
	// The trace is persisted as its own reasoning entry...
	var reasoning map[string]any
	for _, e := range entries {
		if e["kind"] == "reasoning" {
			reasoning = e
		}
	}
	if reasoning == nil {
		t.Fatal("reasoning trace was not persisted")
	}
	if trace, _ := reasoning["content"].(string); !strings.Contains(trace, "respond warmly") {
		t.Errorf("reasoning entry = %+v, want the thinking trace", reasoning)
	}
	// ...and not mixed into the assistant's reply.
	last := entries[len(entries)-1]
	reply, _ := last["content"].(string)
	if last["role"] != "assistant" || strings.Contains(reply, "respond warmly") {
		t.Errorf("reasoning leaked into the assistant reply: %q", reply)
	}
}

// A full context auto-compacts before the turn: after several large messages
// the stream reports a "compacted" event and a summary is on disk.
func TestAutoCompact(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Big chat")
	big := strings.Repeat("word ", 3000) // ~15k chars; a few of these overrun the window

	var lastBody string
	for i := 0; i < 3; i++ {
		b, _ := json.Marshal(map[string]string{"id": id, "content": big})
		resp, err := http.Post(api.URL+"/api/chat/stream", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		buf, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastBody = string(buf)
	}

	if !strings.Contains(lastBody, "event: compacted") {
		t.Error("expected a 'compacted' event once the budget filled")
	}
	entries := getConversation(t, api, id)
	summaries := 0
	for _, e := range entries {
		if e["kind"] == "summary" {
			summaries++
		}
	}
	if summaries == 0 {
		t.Fatal("expected auto-compaction to have folded at least once")
	}
}

// The done event carries the generation speed (llama.cpp timings → tok/s).
func TestChatStreamReportsSpeed(t *testing.T) {
	api := newAPI(t)
	id := createChat(t, api, "Speed")
	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"Hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	if body := buf.String(); !strings.Contains(body, "predictPerSecond") {
		t.Errorf("done event missing tok/s timings:\n%s", body)
	}
}

// A turn against an unknown conversation id is rejected before streaming.
func TestChatStreamUnknownConversation(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"deadbeefdeadbeef","content":"Hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

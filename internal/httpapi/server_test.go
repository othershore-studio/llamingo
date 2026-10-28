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
		Model string `json:"model"`
		NCtx  int    `json:"nCtx"`
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
	if len(entries) != 2 {
		t.Fatalf("persisted %d entries, want 2 (user + assistant): %+v", len(entries), entries)
	}
	if entries[0]["role"] != "user" || entries[0]["content"] != "Hi" {
		t.Errorf("entry 0 = %+v, want the user's 'Hi'", entries[0])
	}
	reply, _ := entries[1]["content"].(string)
	if entries[1]["role"] != "assistant" || !strings.Contains(reply, "Hello") {
		t.Errorf("entry 1 = %+v, want the assistant reply", entries[1])
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

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"bytes"
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

	resp, err := http.Post(api.URL+"/api/chats", "application/json", strings.NewReader(`{"title":"Trip ideas"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create status = %d", resp.StatusCode)
	}

	resp2, err := http.Get(api.URL + "/api/chats")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var chats []map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&chats); err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0]["title"] != "Trip ideas" {
		t.Fatalf("chats = %+v, want one titled 'Trip ideas'", chats)
	}
}

func TestChatStreamSSE(t *testing.T) {
	api := newAPI(t)
	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"messages":[{"role":"user","content":"Hi"}]}`))
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
}

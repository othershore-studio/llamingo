// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita_test

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/taita"
)

const sidCookie = "taita_sid"

func newServer(t *testing.T, ui fs.FS) *taita.Server {
	t.Helper()
	// The chat-list/create endpoints exercised here never call the backend, so a
	// bare one pointed at an unused address is enough (no model, no network).
	srv := taita.New(backend.New("http://127.0.0.1:0"), t.TempDir(), ui)
	t.Cleanup(func() { srv.Close() }) // release the bbolt accounts DB before TempDir cleanup
	return srv
}

// do issues one request, carrying sid as the session cookie when non-empty, and
// returns the recorder plus the session id in effect afterwards (the one just
// minted when sid was empty).
func do(t *testing.T, srv *taita.Server, method, target, sid, body string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if sid != "" {
		r.AddCookie(&http.Cookie{Name: sidCookie, Value: sid})
	}
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, r)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sidCookie {
			sid = c.Value
		}
	}
	return rec, sid
}

func TestMintsSessionCookie(t *testing.T) {
	srv := newServer(t, nil)
	rec, sid := do(t, srv, "GET", "/api/chats", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
	}
	if sid == "" {
		t.Fatal("no session cookie minted on first request")
	}
	// A second request carrying the cookie must NOT mint a new one.
	rec2 := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/chats", nil)
	r.AddCookie(&http.Cookie{Name: sidCookie, Value: sid})
	srv.ServeHTTP(rec2, r)
	if got := rec2.Result().Cookies(); len(got) != 0 {
		t.Fatalf("second request re-set the cookie: %v", got)
	}
}

func TestPerSessionIsolation(t *testing.T) {
	srv := newServer(t, nil)

	// Session A creates a chat.
	rec, sidA := do(t, srv, "POST", "/api/chats", "", `{"title":"A only"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create chat: status = %d; body = %s", rec.Code, rec.Body)
	}

	// Session A sees its chat.
	rec, sidA = do(t, srv, "GET", "/api/chats", sidA, "")
	if n := countChats(t, rec); n != 1 {
		t.Fatalf("session A sees %d chats, want 1", n)
	}

	// A fresh session (no cookie → new id) sees none of A's chats.
	recB, sidB := do(t, srv, "GET", "/api/chats", "", "")
	if sidB == sidA {
		t.Fatal("fresh session reused A's id")
	}
	if n := countChats(t, recB); n != 0 {
		t.Fatalf("session B sees %d chats, want 0 (isolation)", n)
	}
}

func TestServesUI(t *testing.T) {
	ui := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>llaminGO</title>")},
	}
	srv := newServer(t, ui)
	rec, _ := do(t, srv, "GET", "/", "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "llaminGO") {
		t.Fatalf("UI not served: status = %d; body = %s", rec.Code, rec.Body)
	}
}

// countChats parses a GET /api/chats response and returns the number of chats.
func countChats(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list chats: status = %d; body = %s", rec.Code, rec.Body)
	}
	var chats []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &chats); err != nil {
		t.Fatalf("decode chats: %v; body = %s", err, rec.Body)
	}
	return len(chats)
}

// taita's per-user cores run in server mode, which the UI reads from /api/mode to
// drop Projects and show a single server-owned chat list.
func TestServerModeDeclared(t *testing.T) {
	srv := newServer(t, nil)
	rec, _ := do(t, srv, "GET", "/api/mode", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body)
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rec.Body)
	}
	if body.Mode != "server" {
		t.Errorf("mode = %q, want %q (taita cores run in server mode)", body.Mode, "server")
	}
}

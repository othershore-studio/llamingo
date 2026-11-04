// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/taita"
)

// identityResp decodes a /api/whoami or /api/login body. User is a pointer so the
// anonymous {"user": null} case is distinguishable from a name.
type identityResp struct {
	User  *string `json:"user"`
	Role  string  `json:"role"`
	Admin bool    `json:"admin"`
}

func decodeIdentity(t *testing.T, rec *httptest.ResponseRecorder) identityResp {
	t.Helper()
	var id identityResp
	if err := json.Unmarshal(rec.Body.Bytes(), &id); err != nil {
		t.Fatalf("decode identity: %v; body = %s", err, rec.Body)
	}
	return id
}

func TestWhoamiAnonymous(t *testing.T) {
	srv := newServer(t, nil)
	rec, _ := do(t, srv, "GET", "/api/whoami", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", rec.Code, rec.Body)
	}
	if id := decodeIdentity(t, rec); id.User != nil {
		t.Fatalf("anonymous whoami user = %q, want null", *id.User)
	}
}

func TestFirstProfileIsAdminAndNeedsPassword(t *testing.T) {
	srv := newServer(t, nil)

	// The very first profile becomes the admin, and is refused without a password.
	rec, sid := do(t, srv, "POST", "/api/login", "", `{"name":"Dad"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("first login w/o password: status = %d, want 400; body = %s", rec.Code, rec.Body)
	}

	// With a password it succeeds as admin, on the same session.
	rec, sid = do(t, srv, "POST", "/api/login", sid, `{"name":"Dad","password":"s3cret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login: status = %d; body = %s", rec.Code, rec.Body)
	}
	id := decodeIdentity(t, rec)
	if id.User == nil || *id.User != "Dad" || id.Role != "admin" || !id.Admin {
		t.Fatalf("admin identity = %+v", id)
	}

	// whoami on the same session reports the admin.
	rec, _ = do(t, srv, "GET", "/api/whoami", sid, "")
	if id := decodeIdentity(t, rec); id.User == nil || *id.User != "Dad" || !id.Admin {
		t.Fatalf("whoami after login = %+v", id)
	}
}

func TestSecondProfileIsPasswordlessUser(t *testing.T) {
	srv := newServer(t, nil)
	// An admin must exist first (first profile).
	do(t, srv, "POST", "/api/login", "", `{"name":"Dad","password":"s3cret"}`)

	// A fresh session creates a passwordless profile — not first, so no password.
	rec, _ := do(t, srv, "POST", "/api/login", "", `{"name":"kid"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("kid login: status = %d; body = %s", rec.Code, rec.Body)
	}
	id := decodeIdentity(t, rec)
	if id.User == nil || *id.User != "kid" || id.Role != "user" || id.Admin {
		t.Fatalf("kid identity = %+v", id)
	}
}

// TestDesignatedAdmin proves the -admin override: the named account is the admin
// no matter who signs in first (no land-grab), and others are plain users.
func TestDesignatedAdmin(t *testing.T) {
	srv := taita.New(backend.New("http://127.0.0.1:0"), t.TempDir(), nil, taita.WithAdmin("Dad"))
	t.Cleanup(func() { srv.Close() })

	// A non-designated name that signs in first is a plain user (no password needed).
	rec, _ := do(t, srv, "POST", "/api/login", "", `{"name":"mom"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("mom login: status = %d; body = %s", rec.Code, rec.Body)
	}
	if id := decodeIdentity(t, rec); id.Admin || id.Role != "user" {
		t.Fatalf("mom should be a regular user, got %+v", id)
	}

	// The designated name (matched case-insensitively) must set a password, and is admin.
	rec, _ = do(t, srv, "POST", "/api/login", "", `{"name":"dad"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("designated admin without password should be refused, got %d", rec.Code)
	}
	rec, _ = do(t, srv, "POST", "/api/login", "", `{"name":"dad","password":"s3cret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("dad login: status = %d; body = %s", rec.Code, rec.Body)
	}
	if id := decodeIdentity(t, rec); !id.Admin || id.Role != "admin" {
		t.Fatalf("dad should be admin, got %+v", id)
	}
}

func TestWrongPasswordRejected(t *testing.T) {
	srv := newServer(t, nil)
	do(t, srv, "POST", "/api/login", "", `{"name":"Dad","password":"s3cret"}`)

	rec, _ := do(t, srv, "POST", "/api/login", "", `{"name":"Dad","password":"nope"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status = %d, want 401; body = %s", rec.Code, rec.Body)
	}
}

// TestLoginMergesAnonChats proves the claim step: a session's anonymous chats
// follow the user into the account store on login, and move (not copy) — the
// anonymous store is empty afterward.
func TestLoginMergesAnonChats(t *testing.T) {
	srv := newServer(t, nil)

	// As an anonymous session, create a scratch chat.
	rec, sid := do(t, srv, "POST", "/api/chats", "", `{"title":"scratch"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create chat: status = %d; body = %s", rec.Code, rec.Body)
	}
	rec, sid = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 1 {
		t.Fatalf("anonymous sees %d chats, want 1", n)
	}

	// Signing in (first profile → admin) carries the scratch chat into the account.
	rec, sid = do(t, srv, "POST", "/api/login", sid, `{"name":"Dad","password":"s3cret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d; body = %s", rec.Code, rec.Body)
	}
	rec, sid = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 1 {
		t.Fatalf("after login sees %d chats, want 1 (merged in)", n)
	}

	// The chat moved, not copied: the anonymous store is now empty.
	rec, sid = do(t, srv, "POST", "/api/logout", sid, "")
	rec, _ = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 0 {
		t.Fatalf("after logout sees %d chats, want 0 (moved, not copied)", n)
	}
}

// TestLoginSwitchesStoreAndLogout proves login serves the account's own store,
// logout returns to the anonymous store, and the identity (and its chats) persist
// across a re-login — the folder-anchored session model.
func TestLoginSwitchesStoreAndLogout(t *testing.T) {
	srv := newServer(t, nil)

	// Log in as the admin and create a chat in the account store.
	_, sid := do(t, srv, "POST", "/api/login", "", `{"name":"Dad","password":"s3cret"}`)
	rec, sid := do(t, srv, "POST", "/api/chats", sid, `{"title":"admin chat"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create chat: status = %d; body = %s", rec.Code, rec.Body)
	}
	rec, sid = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 1 {
		t.Fatalf("logged-in sees %d chats, want 1", n)
	}

	// Logging out returns to the (empty) anonymous store.
	rec, sid = do(t, srv, "POST", "/api/logout", sid, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d; body = %s", rec.Code, rec.Body)
	}
	rec, sid = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 0 {
		t.Fatalf("after logout sees %d chats, want 0 (anonymous store)", n)
	}

	// Logging back in restores the account's chat.
	rec, sid = do(t, srv, "POST", "/api/login", sid, `{"name":"Dad","password":"s3cret"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-login: status = %d; body = %s", rec.Code, rec.Body)
	}
	rec, _ = do(t, srv, "GET", "/api/chats", sid, "")
	if n := countChats(t, rec); n != 1 {
		t.Fatalf("after re-login sees %d chats, want 1", n)
	}
}

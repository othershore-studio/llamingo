// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// session is the resolved identity for one request: the raw sid, the anon-folder
// key (where the sidecar session.json lives — the session anchor), the store key
// actually served (the anon key, or "users/<name>" when logged in), and the
// logged-in account's name + role ("" / zero when anonymous).
type session struct {
	sid     string
	anonKey string
	key     string
	user    string
	role    Role
}

// sessionMeta is the per-session sidecar (session.json) in the anon folder. It
// records login state as data (Daniel's rule: never filesystem mtime — backups
// and copies mangle dates). User is the claimed account; empty means anonymous.
type sessionMeta struct {
	Created time.Time `json:"created"`
	User    string    `json:"user,omitempty"`
}

// accounts opens the bbolt account registry (data/accounts.db) on first use and
// caches it. Opened lazily so an anon-only (P1) deployment never creates the DB
// file — accounts are a P2 artifact.
func (s *Server) accounts() (Accounts, error) {
	s.accMu.Lock()
	defer s.accMu.Unlock()
	if s.acc != nil || s.accErr != nil {
		return s.acc, s.accErr
	}
	db, err := OpenAccounts(filepath.Join(s.dataRoot, "accounts.db"))
	if err != nil {
		s.accErr = err
		return nil, err
	}
	s.acc = db
	return db, nil
}

// Close releases server-held resources — currently the account registry, if it
// was opened. Per-session cores hold only file stores, which need no explicit
// close. Safe to call more than once.
func (s *Server) Close() error {
	s.accMu.Lock()
	defer s.accMu.Unlock()
	if s.acc == nil {
		return nil
	}
	err := s.acc.Close()
	s.acc = nil
	return err
}

// readSession loads the sidecar for an anon folder. ok is false when it is absent
// or unreadable (treated as a fresh anonymous session).
func (s *Server) readSession(anonKey string) (sessionMeta, bool) {
	root, err := s.rootFor(anonKey)
	if err != nil {
		return sessionMeta{}, false
	}
	b, err := os.ReadFile(filepath.Join(root, "session.json"))
	if err != nil {
		return sessionMeta{}, false
	}
	var m sessionMeta
	if json.Unmarshal(b, &m) != nil {
		return sessionMeta{}, false
	}
	return m, true
}

// writeSession persists the sidecar, creating the anon folder if needed.
func (s *Server) writeSession(anonKey string, m sessionMeta) error {
	root, err := s.rootFor(anonKey)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "session.json"), b, 0o600)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type loginReq struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// handleLogin implements the hybrid "become this profile" model (docs/taita.md
// §6): an existing passwordless name you simply become; a password-protected name
// requires its password; an unknown name is created (passwordless by default, or
// protected if a password is supplied) and becomes yours. The FIRST account is the
// admin and must set a password — it gates the server's own config.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, sess *session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loginReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request body"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "a profile name is required"})
		return
	}
	acc, err := s.accounts()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	a, err := acc.Get(name)
	switch {
	case errors.Is(err, ErrNotFound):
		list, _ := acc.List()
		first := len(list) == 0
		if first && req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error": "the first profile becomes the admin and needs a password",
			})
			return
		}
		a = &Account{Name: name, Role: RoleUser, Created: time.Now().UTC()}
		if first {
			a.Role = RoleAdmin
		}
		if req.Password != "" {
			if err := a.SetPassword(req.Password); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		if err := acc.Put(a); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	default:
		if !a.CheckPassword(req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "wrong password"})
			return
		}
	}

	// Claim: the session's anonymous chats follow the user into the account store.
	// Best-effort — a merge error must never block the login (the chats then just
	// stay under the anon folder).
	_ = s.mergeClaim(sess.anonKey, a.Name)

	// Record the claim on the anchor sidecar so the session resolves to the account.
	m, _ := s.readSession(sess.anonKey)
	if m.Created.IsZero() {
		m.Created = time.Now().UTC()
	}
	m.User = a.Name
	if err := s.writeSession(sess.anonKey, m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, identityJSON(a.Name, a.Role))
}

// handleLogout clears the session's claim, returning it to anonymous. The account
// and its store are untouched.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, sess *session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if m, ok := s.readSession(sess.anonKey); ok && m.User != "" {
		m.User = ""
		_ = s.writeSession(sess.anonKey, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": nil})
}

// handleWhoami reports the session's identity so the UI can show who is signed in
// and whether to offer the admin-only server settings.
func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request, sess *session) {
	if sess.user == "" {
		writeJSON(w, http.StatusOK, map[string]any{"user": nil})
		return
	}
	writeJSON(w, http.StatusOK, identityJSON(sess.user, sess.role))
}

func identityJSON(user string, role Role) map[string]any {
	return map[string]any{"user": user, "role": role, "admin": role == RoleAdmin}
}

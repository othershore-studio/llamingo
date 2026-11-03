// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package taita is the multi-user gateway: one process that fans out to a
// per-user core. Each browser session (an anonymous cookie for now; named
// accounts later) resolves to its own on-disk store under dataRoot, and taita
// serves that session an isolated httpapi.Server over the same API the desktop
// and mobile shells use — plus the embedded chat-only web UI.
//
// taita adds NO code to internal/httpapi: it composes N single-store
// httpapi.Servers rather than teaching one Server about many stores. That keeps
// internal/httpapi — and the desktop / mobile / CLI clients, all of which call
// httpapi.New(be, st) — byte-for-byte unchanged, so the server cannot break a
// client. Per-user isolation (separate permission gates, memory, MCP registries)
// falls out for free, one Server per user.
//
// The llama backend (one connection) and the image-generation queue
// (imagegen.genSem, a process-global semaphore) are shared across every user, so
// there is still exactly one upstream connection and one GPU queue. See the
// memory note taita-build-plan and docs/architecture.md (Taita).
package taita

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

// sessionCookie carries the opaque anonymous-session id. In P1 presence of a
// valid cookie is the whole identity — no login — and the folder it maps to is
// that session's private store (see taita-build-plan, user-not-admin-lens). The
// id is hashed to name the folder, so the on-disk name is never the live
// credential.
const sessionCookie = "taita_sid"

// Server is the multi-user HTTP handler. It routes /api/* to the requesting
// session's own httpapi.Server (created on first use and cached) and everything
// else to the embedded web UI.
type Server struct {
	be       backend.Backend
	dataRoot string
	ui       http.Handler

	mu    sync.Mutex
	cores map[string]*httpapi.Server // keyed by store subpath, e.g. "annon/<hash>"
}

// New builds a gateway. be is the shared llama backend (one connection for every
// user); dataRoot is where per-user stores live; ui serves the built SPA (nil →
// non-API paths 404, which is convenient in tests that only exercise the API).
func New(be backend.Backend, dataRoot string, ui fs.FS) *Server {
	var h http.Handler = http.NotFoundHandler()
	if ui != nil {
		h = http.FileServer(http.FS(ui))
	}
	return &Server{be: be, dataRoot: dataRoot, ui: h, cores: map[string]*httpapi.Server{}}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := s.ensureSession(w, r)
	if strings.HasPrefix(r.URL.Path, "/api/") {
		core, err := s.coreFor(key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		core.ServeHTTP(w, r)
		return
	}
	s.ui.ServeHTTP(w, r)
}

// ensureSession reads the anonymous session cookie, minting one (and setting it
// on the response) when it is absent or malformed, and returns the store subpath
// for that session: "annon/<sha256(sid)>". The raw sid lives only in the cookie;
// only its hash names a folder.
func (s *Server) ensureSession(w http.ResponseWriter, r *http.Request) string {
	sid := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		sid = c.Value
	}
	if !validSID(sid) {
		sid = newSID()
		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookie,
			Value:    sid,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			// Secure is deliberately omitted: on a private mesh taita may be plain
			// http. A public (HTTPS) tunnel should set it — a P2 hardening knob.
		})
	}
	sum := sha256.Sum256([]byte(sid))
	return "annon/" + hex.EncodeToString(sum[:16]) // 128-bit folder name
}

// coreFor returns the httpapi.Server for a store subpath, creating and caching it
// on first use. Each core is a single-store httpapi over a store rooted at
// dataRoot/<key>, sharing the one backend. Creation is cheap: httpapi.New starts
// no goroutines and connects no MCP servers until a turn actually runs them.
func (s *Server) coreFor(key string) (*httpapi.Server, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cores[key]; ok {
		return c, nil
	}
	root, err := s.rootFor(key)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(root)
	if err != nil {
		return nil, err
	}
	c := httpapi.New(s.be, st, httpapi.ServerMode())
	s.cores[key] = c
	return c, nil
}

// rootFor resolves a "/"-separated store subpath to an absolute directory under
// dataRoot, refusing any segment that could escape it. Keys are server-generated
// (e.g. "annon/<hex>"), so this is defense in depth.
func (s *Server) rootFor(key string) (string, error) {
	parts := strings.Split(key, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." || strings.ContainsAny(p, `\/`) {
			return "", fmt.Errorf("taita: bad store key %q", key)
		}
	}
	return filepath.Join(append([]string{s.dataRoot}, parts...)...), nil
}

// validSID reports whether a cookie value is a well-formed session id (16 bytes,
// hex). A malformed or missing value is treated as "no session" and replaced.
func validSID(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func newSID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

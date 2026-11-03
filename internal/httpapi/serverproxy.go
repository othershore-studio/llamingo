// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
)

// taitaLink is a local client sidecar's connection to a remote taita server: the
// base URL plus an http.Client whose cookie jar holds the (anonymous, for now)
// taita session, so repeated calls land in the same server-side folder. It is the
// client half of P3 — the sidecar surfaces the taita's chats as a "Server Chats"
// list alongside the user's local chats. See taita-build-plan → TWO CHAT LISTS.
type taitaLink struct {
	base   string
	client *http.Client
}

// WithTaita links this (local) core to a remote taita at baseURL, so /api/server/*
// proxies there. The cookie jar keeps one session across calls — anonymous today;
// an account login lands later (P2).
func WithTaita(baseURL string) Option {
	return func(s *Server) {
		jar, _ := cookiejar.New(nil)
		s.taita = &taitaLink{base: strings.TrimRight(baseURL, "/"), client: &http.Client{Jar: jar}}
	}
}

// handleServerChats proxies the free-chat list/create to the linked taita. With no
// taita linked it is an empty list — a local-only client has no server chats.
func (s *Server) handleServerChats(w http.ResponseWriter, r *http.Request) {
	if s.taita == nil {
		writeJSON(w, []any{})
		return
	}
	s.proxyToTaita(w, r, "/api/chats")
}

// proxyToTaita forwards the request to the linked taita at path, carrying the
// session cookie jar so the server resolves the same folder every time. This is a
// live proxy; the download-by-chat cache (the two-root "cache, not sync") is a
// later refinement.
func (s *Server) proxyToTaita(w http.ResponseWriter, r *http.Request, path string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, s.taita.base+path, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := s.taita.client.Do(req)
	if err != nil {
		http.Error(w, "taita unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

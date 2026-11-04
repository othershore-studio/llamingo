// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
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

// WithTaita links this (local) core to a remote taita at baseURL at construction.
// The persisted setting (config.TaitaURL, edited in the Settings window) is the
// normal path; this option is for tests and explicit wiring.
func WithTaita(baseURL string) Option {
	return func(s *Server) { s.setTaita(baseURL) }
}

// setTaita points this sidecar at a taita, or clears it when url is empty,
// preserving the live session jar when the URL is unchanged. Safe for concurrent
// use: the Settings UI relinks at runtime (PUT /api/settings).
func (s *Server) setTaita(url string) {
	url = strings.TrimRight(strings.TrimSpace(url), "/")
	s.taitaMu.Lock()
	defer s.taitaMu.Unlock()
	switch {
	case url == "":
		s.taita = nil
	case s.taita != nil && s.taita.base == url:
		// unchanged — keep the live session jar
	default:
		jar, _ := cookiejar.New(nil)
		s.taita = &taitaLink{base: url, client: &http.Client{Jar: jar}}
	}
}

// taitaLinked reports whether a taita is currently linked (surfaced at /api/mode
// so the UI shows the Server Chats list).
func (s *Server) taitaLinked() bool {
	s.taitaMu.Lock()
	defer s.taitaMu.Unlock()
	return s.taita != nil
}

// handleServerChats proxies the free-chat list/create to the linked taita. With no
// taita linked it is an empty list — a local-only client has no server chats.
func (s *Server) handleServerChats(w http.ResponseWriter, r *http.Request) {
	s.taitaMu.Lock()
	link := s.taita
	s.taitaMu.Unlock()
	if link == nil {
		writeJSON(w, []any{})
		return
	}
	s.proxyToTaita(w, r, link, "/api/chats")
}

// currentTaita returns the linked taita, or nil, safely.
func (s *Server) currentTaita() *taitaLink {
	s.taitaMu.Lock()
	defer s.taitaMu.Unlock()
	return s.taita
}

// proxyServer returns a handler that forwards a /api/server/* request to the linked
// taita at the path pathFn builds from it — the two-worlds proxy, where a server
// chat's every op (open, stream, budget, tools, memory, props) runs on taita. With
// no taita linked it is a 502. proxyToTaita streams and flushes, so an SSE turn is
// live. See docs/taita.md: the active chat's origin is the only router.
func (s *Server) proxyServer(pathFn func(*http.Request) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		link := s.currentTaita()
		if link == nil {
			http.Error(w, "no taita linked", http.StatusBadGateway)
			return
		}
		s.proxyToTaita(w, r, link, pathFn(r))
	}
}

// serverConvPath / serverConvToolsPath / serverConvBudgetPath build the taita path
// for a server chat's conversation endpoints, carrying the {id} (and the budget's
// ?fresh query) through.
func serverConvPath(r *http.Request) string { return "/api/conversations/" + r.PathValue("id") }
func serverConvToolsPath(r *http.Request) string {
	return "/api/conversations/" + r.PathValue("id") + "/tools"
}
func serverConvBudgetPath(r *http.Request) string {
	p := "/api/conversations/" + r.PathValue("id") + "/budget"
	if r.URL.RawQuery != "" {
		p += "?" + r.URL.RawQuery
	}
	return p
}

// proxyToTaita forwards the request to the linked taita at path, carrying its
// session cookie jar so the server resolves the same folder every time. Live proxy
// for now; the download-by-chat cache (two-root "cache, not sync") is a later step.
func (s *Server) proxyToTaita(w http.ResponseWriter, r *http.Request, link *taitaLink, path string) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, link.base+path, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := link.client.Do(req)
	if err != nil {
		http.Error(w, "taita unreachable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	// Stream the body through, flushing so a server chat's SSE turn reaches the
	// browser live rather than buffering until the connection closes.
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}

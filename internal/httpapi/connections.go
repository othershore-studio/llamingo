// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/store"
)

// GET /api/connections — the saved llama-servers the user switches between, and
// which URL is active now. The active connection (the live backend) is always
// included, synthesized from the backend when the user hasn't saved it yet, so
// the status-bar picker can always show where the app is pointed.
func (s *Server) handleGetConnections(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	cfg, _ := s.st.Config()
	activeURL := rc.BaseURL()
	conns := cfg.Connections
	if activeURL != "" && !containsURL(conns, activeURL) {
		conns = append([]store.Connection{{Name: defaultConnName(activeURL), URL: activeURL, Token: rc.Token()}}, conns...)
	}
	writeJSON(w, map[string]any{"activeUrl": activeURL, "connections": conns})
}

// PUT /api/connections {connections:[{name,url,token}]} — save the list (the
// Settings card). Does not switch the active connection; that is /activate. Rows
// without a URL are dropped; an unnamed row is labeled from its host.
func (s *Server) handlePutConnections(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Connections []store.Connection `json:"connections"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	clean := make([]store.Connection, 0, len(body.Connections))
	for _, c := range body.Connections {
		c.Name, c.URL, c.Token = strings.TrimSpace(c.Name), strings.TrimSpace(c.URL), strings.TrimSpace(c.Token)
		if c.URL == "" {
			continue
		}
		if c.Name == "" {
			c.Name = defaultConnName(c.URL)
		}
		clean = append(clean, c)
	}
	cfg, _ := s.st.Config()
	cfg.Connections = clean
	if err := s.st.SetConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/connections/activate {url, token} — point the live backend at this
// connection and persist it as active, so it survives a restart. The next /props
// poll reflects the new model; the client re-reads /props after this returns.
func (s *Server) handleActivateConnection(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	var body struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	url := strings.TrimSpace(body.URL)
	if url == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	cfg, _ := s.st.Config()
	cfg.URL, cfg.Token = url, strings.TrimSpace(body.Token)
	if err := s.st.SetConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rc.SetConnection(url, cfg.Token)
	w.WriteHeader(http.StatusNoContent)
}

func containsURL(conns []store.Connection, url string) bool {
	for _, c := range conns {
		if c.URL == url {
			return true
		}
	}
	return false
}

// defaultConnName labels a connection by its host (and port) when the user hasn't
// named it — e.g. "http://localhost:8080/" → "localhost:8080".
func defaultConnName(url string) string {
	h := strings.TrimPrefix(strings.TrimPrefix(url, "https://"), "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if h == "" {
		return url
	}
	return h
}

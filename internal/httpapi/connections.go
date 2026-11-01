// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/store"
)

// GET /api/connections — the saved llama-servers the user switches between, and
// which one (by id) is active now. Connections are tracked by a stable id, not
// their url, so two connections on the SAME server (personas — a low-temp "Coder"
// and a high-temp "Creative") are distinguishable and survive url edits. The
// active connection is always surfaced, synthesized from the live backend when the
// user hasn't saved it yet. Assigns ids to any legacy connection lacking one (a
// one-time migration).
func (s *Server) handleGetConnections(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	cfg, _ := s.st.Config()
	dirty := false
	for i := range cfg.Connections {
		if cfg.Connections[i].ID == "" {
			cfg.Connections[i].ID = newConnID()
			dirty = true
		}
	}
	activeURL := rc.BaseURL()
	activeID := cfg.ActiveID
	// Legacy: no active id yet — adopt the saved connection matching the live
	// url + sampling, so the right one lights up without a re-activate.
	if activeID == "" && activeURL != "" {
		for _, c := range cfg.Connections {
			if c.URL == activeURL && sameFloat(c.Temperature, cfg.Temperature) && sameFloat(c.MinP, cfg.MinP) {
				activeID = c.ID
				break
			}
		}
		if activeID != "" {
			cfg.ActiveID = activeID
			dirty = true
		}
	}
	conns := cfg.Connections
	if activeURL != "" && !containsID(conns, activeID) {
		// The active connection isn't saved — surface it under a synthetic id.
		conns = append([]store.Connection{{ID: "active", Name: defaultConnName(activeURL), URL: activeURL, Token: rc.Token(), Temperature: cfg.Temperature, MinP: cfg.MinP}}, conns...)
		activeID = "active"
	}
	if dirty {
		_ = s.st.SetConfig(cfg)
	}
	writeJSON(w, map[string]any{"activeId": activeID, "activeUrl": activeURL, "connections": conns})
}

// PUT /api/connections {connections:[{id,name,url,token,temperature,minP}]} — save
// the list (the Settings card). Does not switch the active connection; that is
// /activate. Rows without a url are dropped; an unnamed row is labeled from its
// host; a row without an id gets one.
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
		c.ID, c.Name, c.URL, c.Token = strings.TrimSpace(c.ID), strings.TrimSpace(c.Name), strings.TrimSpace(c.URL), strings.TrimSpace(c.Token)
		c.SystemPrompt = strings.TrimSpace(c.SystemPrompt)
		if c.URL == "" {
			continue
		}
		if c.Name == "" {
			c.Name = defaultConnName(c.URL)
		}
		if c.ID == "" {
			c.ID = newConnID()
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

// POST /api/connections/activate {id, url, token, temperature, minP} — point the
// live backend at this connection, with its sampling, and persist it (by id) as
// active so it survives a restart. Tracking by id (not url) is what makes two
// connections on the SAME url behave as distinct personas. The next /props poll
// reflects the new model; the client re-reads /props after this returns.
func (s *Server) handleActivateConnection(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	var body struct {
		ID           string   `json:"id"`
		URL          string   `json:"url"`
		Token        string   `json:"token"`
		Temperature  *float64 `json:"temperature"`
		MinP         *float64 `json:"minP"`
		SystemPrompt string   `json:"systemPrompt"`
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
	cfg.ActiveID = strings.TrimSpace(body.ID)
	cfg.URL, cfg.Token = url, strings.TrimSpace(body.Token)
	cfg.Temperature, cfg.MinP = body.Temperature, body.MinP
	cfg.SystemPrompt = strings.TrimSpace(body.SystemPrompt)
	if err := s.st.SetConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rc.SetConnection(url, cfg.Token)
	rc.SetSampling(body.Temperature, body.MinP)
	w.WriteHeader(http.StatusNoContent)
}

func containsID(conns []store.Connection, id string) bool {
	if id == "" {
		return false
	}
	for _, c := range conns {
		if c.ID == id {
			return true
		}
	}
	return false
}

func sameFloat(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// newConnID returns a short random id for a saved connection.
func newConnID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
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

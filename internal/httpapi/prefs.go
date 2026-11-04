// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/store"
)

// GET /api/prefs — the user's own preferences (per-user, in their data store):
// location and a personal system prompt. Unlike /api/settings (the admin's server
// config), these are everyone's to set, so taita does not admin-gate this route.
func (s *Server) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	p, _ := s.st.Prefs()
	writeJSON(w, map[string]any{"location": p.Location, "systemPrompt": p.SystemPrompt})
}

// PUT /api/prefs {location, systemPrompt} — save the user's own preferences.
func (s *Server) handlePutPrefs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Location     string `json:"location"`
		SystemPrompt string `json:"systemPrompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.st.SetPrefs(store.Prefs{
		Location:     strings.TrimSpace(body.Location),
		SystemPrompt: strings.TrimSpace(body.SystemPrompt),
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

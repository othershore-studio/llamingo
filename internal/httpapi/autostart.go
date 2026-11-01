// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/othershore-studio/llamingo/internal/autostart"
)

// GET /api/autostart — whether this build can launch llaminGO at login, and the
// current state (enabled, and whether the login launch starts minimized to the
// tray). The desktop app shows the Startup controls only when supported is true;
// the browser never calls this.
func (s *Server) handleGetAutostart(w http.ResponseWriter, r *http.Request) {
	st, err := autostart.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, st)
}

// PUT /api/autostart {enabled, minimized} — turn login autostart on (optionally
// starting minimized to the tray) or off. The OS entry is the source of truth, so
// there's nothing to persist here beyond the write itself.
func (s *Server) handlePutAutostart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled   bool `json:"enabled"`
		Minimized bool `json:"minimized"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var err error
	if body.Enabled {
		err = autostart.Enable(body.Minimized)
	} else {
		err = autostart.Disable()
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Echo the resulting state back so the client reflects what actually took.
	st, err := autostart.Status()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, st)
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
)

// GET /api/memory — the personal memory and whether it was edited outside the app.
func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	content, tampered, err := s.st.Memory()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"content": content, "tampered": tampered})
}

// PUT /api/memory {content} — replace the personal memory (the user editing it).
func (s *Server) handlePutMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.st.WriteMemory(body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// POST /api/memory/ack — accept an outside edit, clearing the tamper flag.
func (s *Server) handleAckMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.st.AckMemory(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// segmentsFor builds the metered prompt segments for a turn: the personal memory
// (when non-empty) as its own "memory" segment, then the conversation. Keeping
// memory a distinct segment is what lets the budget report its cost honestly.
func (s *Server) segmentsFor(messages []backend.Message) []contextbudget.Segment {
	var segs []contextbudget.Segment
	if mem, _, err := s.st.Memory(); err == nil && strings.TrimSpace(mem) != "" {
		segs = append(segs, contextbudget.Segment{
			Name:     "memory",
			Messages: []backend.Message{{Role: backend.RoleSystem, Content: "Personal memory:\n" + mem}},
		})
	}
	return append(segs, contextbudget.Segment{Name: "conversation", Messages: messages})
}

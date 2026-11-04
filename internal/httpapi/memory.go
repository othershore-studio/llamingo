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

// segmentsFor builds the metered prompt segments: the persona system prompt and
// the working-directory note (when applicable), the personal memory (when
// non-empty), the offered tools (when any), then the conversation — each a
// distinct segment so the budget reports its cost honestly. The tools are
// rendered as a system message of their schemas, a close estimate of what the
// chat template injects (the authoritative count folds in once a turn runs); the
// tool schemas are a real chunk of the budget, so a chat that offers them reads
// honestly even before its first message. Pass nil tools to meter without them.
// convID lets a project session add its working-directory note (""/free chat: none).
func (s *Server) segmentsFor(convID string, messages []backend.Message, tools []backend.ToolDef) []contextbudget.Segment {
	var segs []contextbudget.Segment
	// The active connection's system prompt (a persona's instructions), prepended as
	// a system message and metered like any other segment. Empty = none.
	if cfg, err := s.cfg.Config(); err == nil {
		if sp := strings.TrimSpace(cfg.SystemPrompt); sp != "" {
			segs = append(segs, contextbudget.Segment{Name: "system", Messages: []backend.Message{{Role: backend.RoleSystem, Content: sp}}})
		}
	}
	// In a project session, tell the model its working directory so it uses relative
	// paths (the file tools are confined to this folder). A project can opt out.
	if note := s.workingDirNote(convID); note != "" {
		segs = append(segs, contextbudget.Segment{Name: "workdir", Messages: []backend.Message{{Role: backend.RoleSystem, Content: note}}})
	}
	if mem, _, err := s.st.Memory(); err == nil && strings.TrimSpace(mem) != "" {
		segs = append(segs, contextbudget.Segment{
			Name:     "memory",
			Messages: []backend.Message{{Role: backend.RoleSystem, Content: "Personal memory:\n" + mem}},
		})
	}
	if len(tools) > 0 {
		if b, err := json.Marshal(tools); err == nil {
			segs = append(segs, contextbudget.Segment{
				Name:     "tools",
				Messages: []backend.Message{{Role: backend.RoleSystem, Content: "Tools available:\n" + string(b)}},
			})
		}
	}
	return append(segs, contextbudget.Segment{Name: "conversation", Messages: messages})
}

// workingDirNote is the environment note a project session prepends so the model
// knows where it is working and refers to files by relative paths. Empty for a
// free chat (no project folder) or when the project has opted the note out.
func (s *Server) workingDirNote(convID string) string {
	root, ok, err := s.st.ProjectRoot(convID)
	if err != nil || !ok || root == "" {
		return ""
	}
	if show, err := s.st.ProjectShowWorkingDir(root); err == nil && !show {
		return ""
	}
	return "Your working directory is " + root + " — refer to files by paths relative to it."
}

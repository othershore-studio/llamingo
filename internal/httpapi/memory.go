// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/mcp"
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
	// The user's own standing instructions (per-user prefs), added after the
	// connection's persona segment so the connection's character leads and the
	// user's preferences layer on top. Empty = none.
	if p, err := s.st.Prefs(); err == nil {
		if up := strings.TrimSpace(p.SystemPrompt); up != "" {
			segs = append(segs, contextbudget.Segment{Name: "user-prompt", Messages: []backend.Message{{Role: backend.RoleSystem, Content: up}}})
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
	// The connected MCP servers' own usage guidance (from their handshake), so a
	// small model learns how to drive their tools — llama.cpp injects none of it.
	if instr := s.mcpInstructions(convID); instr != "" {
		segs = append(segs, contextbudget.Segment{
			Name:     "mcp-instructions",
			Messages: []backend.Message{{Role: backend.RoleSystem, Content: instr}},
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

// mcpInstructions gathers the handshake usage guidance from the connected GLOBAL
// MCP servers whose tools are still enabled in this chat — the server authors'
// own coaching — as one system message, so a small model learns how to drive
// those tools (llama.cpp injects none of it). Project servers are left out here
// (reading them would trip the project gate); global servers are trusted.
func (s *Server) mcpInstructions(convID string) string {
	reg, ok := s.globalServers().(*mcp.ServerRegistry)
	if !ok {
		return ""
	}
	disabled, _ := s.st.ConversationTools(convID)
	var parts []string
	for _, si := range reg.Instructions(toSet(disabled)) {
		parts = append(parts, fmt.Sprintf("How to use the %q tools:\n%s", si.Server, si.Text))
	}
	return strings.Join(parts, "\n\n")
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

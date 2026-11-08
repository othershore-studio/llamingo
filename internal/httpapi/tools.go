// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/othershore-studio/llamingo/internal/fetchtool"
	"github.com/othershore-studio/llamingo/internal/filetools"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
)

// toSet turns a name list into a lookup set (nil for an empty list, so Curate
// treats it as a no-op).
func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// toolItem is one advertised tool as the per-chat curation UI needs it.
type toolItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

// toolGroup groups tools by source for the UI: one "system" group (the built-in
// tools) and one "mcp" group per connected server.
type toolGroup struct {
	Kind  string     `json:"kind"`            // "system" | "mcp"
	Name  string     `json:"name"`            //
	Scope string     `json:"scope,omitempty"` // "project" for a project's own MCP server (else global)
	Tools []toolItem `json:"tools"`
}

func toolItemsOf(reg mcp.Registry, disabled map[string]bool) []toolItem {
	if reg == nil {
		return nil
	}
	var items []toolItem
	for _, t := range reg.Tools() {
		items = append(items, toolItem{Name: t.Name, Description: t.Description, Enabled: !disabled[t.Name]})
	}
	return items
}

// mcpGroupsOf turns a ServerRegistry's servers into one group each; scope tags a
// project's own servers so the UI can tell them from the global ones.
func mcpGroupsOf(reg mcp.Registry, disabled map[string]bool, scope string) []toolGroup {
	sr, ok := reg.(*mcp.ServerRegistry)
	if !ok {
		return nil
	}
	var out []toolGroup
	for _, g := range sr.ToolsByServer() {
		grp := toolGroup{Kind: "mcp", Name: g.Server, Scope: scope}
		for _, t := range g.Tools {
			grp.Tools = append(grp.Tools, toolItem{Name: t.Name, Description: t.Description, Enabled: !disabled[t.Name]})
		}
		if len(grp.Tools) > 0 {
			out = append(out, grp)
		}
	}
	return out
}

// GET /api/conversations/{id}/tools — the tools offered in this chat, grouped into
// the built-in "System" tools and one group per MCP server, each marked enabled or
// disabled for this chat. A small local model drowns in too many tools, so each
// chat carries its own trimmed set (stored in its meta.json).
func (s *Server) handleGetConversationTools(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	disabledList, err := s.st.ConversationTools(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	disabled := toSet(disabledList)
	root, hasRoot, _ := s.st.ProjectRoot(id)
	var groups []toolGroup

	// System tools: memory + local always; the project file tools when this chat
	// has a folder; image generation when configured. Tools() uses no gate.
	sys := toolGroup{Kind: "system", Name: "System"}
	sys.Tools = append(sys.Tools, toolItemsOf(memtool.NewTool(s.st, nil), disabled)...)
	sys.Tools = append(sys.Tools, toolItemsOf(localtools.NewTool(s.locationSetting), disabled)...)
	sys.Tools = append(sys.Tools, toolItemsOf(fetchtool.NewTool(nil), disabled)...)
	if hasRoot && root != "" {
		sys.Tools = append(sys.Tools, toolItemsOf(filetools.NewTool(root, nil, nil), disabled)...)
	}
	if tool := s.imageTool(id, nil); tool != nil {
		sys.Tools = append(sys.Tools, toolItemsOf(tool, disabled)...)
	}
	if len(sys.Tools) > 0 {
		groups = append(groups, sys)
	}

	// MCP servers, grouped by server: the global ones, then this project's own
	// (from its .mcp.json), so project tools can be curated per chat too.
	groups = append(groups, mcpGroupsOf(s.globalServers(), disabled, "")...)
	if hasRoot && root != "" {
		groups = append(groups, mcpGroupsOf(s.projectServers(root), disabled, "project")...)
	}

	if groups == nil {
		groups = []toolGroup{}
	}
	writeJSON(w, map[string]any{"groups": groups})
}

// PUT /api/conversations/{id}/tools {disabled:[names]} — persist which tools are
// curated off for this chat.
func (s *Server) handlePutConversationTools(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.st.SetConversationTools(r.PathValue("id"), body.Disabled); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

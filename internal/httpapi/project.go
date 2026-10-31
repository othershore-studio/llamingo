// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/othershore-studio/llamingo/internal/filetools"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
)

// GET /api/project?conv=<id> — a project session's own settings for the ⋮ Project
// surface: the system tools a new session in the project starts with (default
// on/off, stored in .llamingo/project.json), plus the project's own MCP servers
// (read-only — edited in the repo's .mcp.json). A free chat returns inProject=false.
func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	conv := r.URL.Query().Get("conv")
	root, ok, _ := s.st.ProjectRoot(conv)
	if !ok || root == "" {
		writeJSON(w, map[string]any{"inProject": false})
		return
	}
	defaults, _ := s.st.ProjectToolDefaults(root)
	disabled := toSet(defaults)

	// The system tools, each with its default-on state for this project.
	system := make([]toolItem, 0, 6)
	system = append(system, toolItemsOf(memtool.NewTool(s.st, nil), disabled)...)
	system = append(system, toolItemsOf(localtools.NewTool(s.locationSetting), disabled)...)
	system = append(system, toolItemsOf(filetools.NewTool(root), disabled)...)
	if tool := s.imageTool(conv, nil); tool != nil {
		system = append(system, toolItemsOf(tool, disabled)...)
	}

	// The project's own MCP servers, read-only (file-managed).
	pservers, _ := mcp.LoadServers(projectMCPPath(root))
	pmcp := scopeView(pservers, connectedNames(s.projectServers(root)), "project", false)

	writeJSON(w, map[string]any{"inProject": true, "root": root, "systemTools": system, "mcp": pmcp})
}

// PUT /api/project?conv=<id> {disabledSystem:[names]} — persist the system tools a
// new session in this project starts with off (to .llamingo/project.json).
func (s *Server) handlePutProject(w http.ResponseWriter, r *http.Request) {
	root, ok, _ := s.st.ProjectRoot(r.URL.Query().Get("conv"))
	if !ok || root == "" {
		http.Error(w, "not a project session", http.StatusBadRequest)
		return
	}
	var body struct {
		DisabledSystem []string `json:"disabledSystem"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.st.SetProjectToolDefaults(root, body.DisabledSystem); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

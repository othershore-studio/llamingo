// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/othershore-studio/llamingo/internal/imagegen"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
)

// toolView is one advertised tool as the curation UI needs it.
type toolView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
}

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

// GET /api/tools — every tool currently offered to the model (the MCP servers'
// plus the native ones), each marked enabled or disabled, for the curation UI. A
// small local model drowns in too many tools, so the user can turn some off.
func (s *Server) handleGetTools(w http.ResponseWriter, r *http.Request) {
	cfg, _ := s.st.Config()
	disabled := toSet(cfg.DisabledTools)
	var tools []toolView
	seen := map[string]bool{}
	add := func(reg mcp.Registry) {
		if reg == nil {
			return
		}
		for _, t := range reg.Tools() {
			if seen[t.Name] {
				continue
			}
			seen[t.Name] = true
			tools = append(tools, toolView{Name: t.Name, Description: t.Description, Enabled: !disabled[t.Name]})
		}
	}
	// Mirror the sources registryFor assembles (minus the project-scoped file
	// tools, which are contextual): the global MCP servers and the always-present
	// native tools. Tools() uses no gate, so a nil one is fine for listing.
	add(s.globalServers())
	add(memtool.NewTool(s.st, nil))
	add(localtools.NewTool(s.locationSetting))
	if url := s.forgeBaseURL(""); url != "" {
		if imgCfg, err := imagegen.LoadConfig(s.imagegenConfigPath()); err == nil && len(imgCfg.Models) > 0 {
			add(imagegen.NewTool(imagegen.NewForgeNeo(url), nil, nil, imgCfg))
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	if tools == nil {
		tools = []toolView{}
	}
	writeJSON(w, map[string]any{"tools": tools})
}

// PUT /api/tools {disabled:[names]} — persist which tools are curated off. Only
// this field of the config changes; the connection and the rest are preserved.
func (s *Server) handlePutTools(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Disabled []string `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cfg, _ := s.st.Config()
	cfg.DisabledTools = body.Disabled
	if err := s.st.SetConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

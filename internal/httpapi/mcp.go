// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

// mcpServerView is one configured MCP server as the Settings UI needs it. URL-based
// servers (streamable-HTTP / SSE) are editable in the UI; stdio servers are
// command-based and stay file-managed (shown read-only).
type mcpServerView struct {
	Name      string `json:"name"`
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Header    string `json:"header"`
	Editable  bool   `json:"editable"`
	Connected bool   `json:"connected"`
	Scope     string `json:"scope"` // "global" | "project"
}

// mcpServersView lists the configured servers: the global ones, plus (when convID
// is a project session) that project's own .mcp.json servers. Each is marked with
// its scope and whether it is currently connected. Global URL-based servers are
// UI-editable; project servers (a repo file) and stdio servers are read-only.
func (s *Server) mcpServersView(convID string) []mcpServerView {
	gservers, _ := mcp.LoadServers(globalMCPPath(s.st.Root()))
	out := scopeView(gservers, connectedNames(s.globalServers()), "global", true)
	if convID != "" {
		if root, ok, _ := s.st.ProjectRoot(convID); ok && root != "" {
			pservers, _ := mcp.LoadServers(projectMCPPath(root))
			out = append(out, scopeView(pservers, connectedNames(s.projectServers(root)), "project", false)...)
		}
	}
	return out
}

// scopeView turns a server map into views at one scope. editable applies only to
// URL-based servers (stdio is always command-based, so file-managed).
func scopeView(servers map[string]mcp.ServerConfig, connected map[string]bool, scope string, editable bool) []mcpServerView {
	out := make([]mcpServerView, 0, len(servers))
	for name, cfg := range servers {
		out = append(out, mcpServerView{
			Name:      name,
			Transport: cfg.Transport(),
			URL:       cfg.URL,
			Header:    cfg.Headers["Authorization"],
			Editable:  editable && cfg.URL != "",
			Connected: connected[name],
			Scope:     scope,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// connectedNames is the set of server names currently connected in reg.
func connectedNames(reg mcp.Registry) map[string]bool {
	names := map[string]bool{}
	if sr, ok := reg.(*mcp.ServerRegistry); ok {
		for _, g := range sr.ToolsByServer() {
			names[g.Server] = true
		}
	}
	return names
}

// GET /api/mcp[?conv=<id>] — the configured MCP servers for the Settings UI; with
// conv set to a project session, its project-scope servers are included too.
func (s *Server) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"servers": s.mcpServersView(r.URL.Query().Get("conv"))})
}

// PUT /api/mcp {servers:[{name, url, header}]} — replace the URL-based servers with
// this set (keeping any stdio servers from the file), persist to mcp.json, then
// reconnect so the change applies on the next turn — no app restart.
func (s *Server) handlePutMCP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Servers []struct {
			Name   string `json:"name"`
			URL    string `json:"url"`
			Header string `json:"header"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	path := globalMCPPath(s.st.Root())
	existing, _ := mcp.LoadServers(path)
	next := map[string]mcp.ServerConfig{}
	// Preserve command-based (stdio) servers — those stay edited in the file.
	for name, cfg := range existing {
		if cfg.URL == "" {
			next[name] = cfg
		}
	}
	for _, sv := range body.Servers {
		name := strings.TrimSpace(sv.Name)
		url := strings.TrimSpace(sv.URL)
		if name == "" || url == "" {
			continue
		}
		cfg := mcp.ServerConfig{URL: url, Type: "http"}
		if ex, ok := existing[name]; ok && ex.Type != "" {
			cfg.Type = ex.Type // keep an existing server's transport (e.g. sse)
		}
		if h := strings.TrimSpace(sv.Header); h != "" {
			cfg.Headers = map[string]string{"Authorization": h}
		}
		next[name] = cfg
	}
	if err := mcp.SaveServers(path, next); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.reloadGlobalServers()
	writeJSON(w, map[string]any{"servers": s.mcpServersView(r.URL.Query().Get("conv"))})
}

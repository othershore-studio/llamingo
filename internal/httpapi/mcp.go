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
}

// mcpServersView lists the configured servers, marking which are currently
// connected (present in the live registry).
func (s *Server) mcpServersView() []mcpServerView {
	servers, _ := mcp.LoadServers(globalMCPPath(s.st.Root()))
	connected := map[string]bool{}
	if sr, ok := s.globalServers().(*mcp.ServerRegistry); ok {
		for _, g := range sr.ToolsByServer() {
			connected[g.Server] = true
		}
	}
	out := make([]mcpServerView, 0, len(servers))
	for name, cfg := range servers {
		out = append(out, mcpServerView{
			Name:      name,
			Transport: cfg.Transport(),
			URL:       cfg.URL,
			Header:    cfg.Headers["Authorization"],
			Editable:  cfg.URL != "", // URL-based are UI-editable; stdio (command) is file-only
			Connected: connected[name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// GET /api/mcp — the configured MCP servers (name, transport, URL, auth header,
// whether connected) for the Settings UI.
func (s *Server) handleGetMCP(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"servers": s.mcpServersView()})
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
	writeJSON(w, map[string]any{"servers": s.mcpServersView()})
}

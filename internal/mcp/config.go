// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package mcp implements the client-owned MCP loop: configuration (this file),
// a JSON-RPC client to MCP servers, and the tool-call loop that ties the model
// to those servers. See docs/architecture.md → "MCP — the client owns the loop"
// and "Configuration, trust & the frontier hand-off".
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// ServerConfig is one entry under "mcpServers", matching Claude Code's schema:
// a stdio server (Command/Args/Env) or a remote one (Type "http"/"sse" + URL).
type ServerConfig struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Type    string            `json:"type,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Transport reports how to reach the server: "stdio", "http", or "sse".
func (c ServerConfig) Transport() string {
	switch {
	case c.Command != "":
		return "stdio"
	case c.Type == "sse":
		return "sse"
	case c.URL != "":
		return "http"
	default:
		return "stdio"
	}
}

// Scope is where a server was declared, which drives trust: global servers are
// user-authored (trusted); project servers come from the repo and must clear
// the permission gate before first run (the gate lands in increment #4).
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Server is a configured MCP server with its scope.
type Server struct {
	Name   string
	Config ServerConfig
	Scope  Scope
}

// TrustedByDefault reports whether the server may run without an explicit grant:
// global (user-authored) yes, project (from the repo) no.
func (s Server) TrustedByDefault() bool { return s.Scope == ScopeGlobal }

type configFile struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

// Load reads the global and project mcp.json files (either may be absent) and
// returns the merged server set, sorted by name. A project server overrides a
// global one of the same name (project is more specific) and carries project
// scope — so overriding a trusted global name does not inherit its trust.
func Load(globalPath, projectPath string) ([]Server, error) {
	byName := map[string]Server{}

	global, err := readConfig(globalPath)
	if err != nil {
		return nil, err
	}
	for name, cfg := range global {
		byName[name] = Server{Name: name, Config: cfg, Scope: ScopeGlobal}
	}

	project, err := readConfig(projectPath)
	if err != nil {
		return nil, err
	}
	for name, cfg := range project {
		byName[name] = Server{Name: name, Config: cfg, Scope: ScopeProject}
	}

	out := make([]Server, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func readConfig(path string) (map[string]ServerConfig, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: read %s: %w", path, err)
	}
	var f configFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("mcp: parse %s: %w", path, err)
	}
	return f.MCPServers, nil
}

// LoadServers reads one mcp.json and returns its server map (nil if the file is
// absent). The UI reads this to list and edit the configured servers.
func LoadServers(path string) (map[string]ServerConfig, error) {
	return readConfig(path)
}

// SaveServers writes the server map to path as mcp.json (the "mcpServers" object),
// so a UI edit persists to the same file a user could hand-edit.
func SaveServers(path string, servers map[string]ServerConfig) error {
	b, err := json.MarshalIndent(configFile{MCPServers: servers}, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: marshal config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("mcp: write %s: %w", path, err)
	}
	return nil
}

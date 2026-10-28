// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

func TestLoadGlobalAndProject(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "mcp.json")
	project := filepath.Join(dir, ".mcp.json")
	mustWrite(t, global, `{"mcpServers":{
		"forge":  {"command":"forge-mcp","args":["--stdio"]},
		"shared": {"command":"global-one"}
	}}`)
	mustWrite(t, project, `{"mcpServers":{
		"repo":   {"type":"http","url":"https://host/mcp"},
		"shared": {"command":"project-one"}
	}}`)

	servers, err := mcp.Load(global, project)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]mcp.Server{}
	for _, s := range servers {
		byName[s.Name] = s
	}
	if len(servers) != 3 {
		t.Fatalf("want 3 servers (forge, shared, repo), got %d: %+v", len(servers), servers)
	}

	if forge := byName["forge"]; forge.Scope != mcp.ScopeGlobal || !forge.TrustedByDefault() || forge.Config.Transport() != "stdio" {
		t.Errorf("forge = %+v (want global, trusted, stdio)", forge)
	}
	if repo := byName["repo"]; repo.Scope != mcp.ScopeProject || repo.TrustedByDefault() || repo.Config.Transport() != "http" {
		t.Errorf("repo = %+v (want project, untrusted, http)", repo)
	}
	// Project override wins and does not inherit the global server's trust.
	if sh := byName["shared"]; sh.Scope != mcp.ScopeProject || sh.TrustedByDefault() || sh.Config.Command != "project-one" {
		t.Errorf("shared = %+v (want project override, untrusted, command project-one)", sh)
	}
}

func TestLoadMissingFilesIsEmpty(t *testing.T) {
	servers, err := mcp.Load(filepath.Join(t.TempDir(), "absent.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 0 {
		t.Fatalf("want 0 servers from absent files, got %d", len(servers))
	}
}

func TestLoadMalformedIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mcp.json")
	mustWrite(t, p, `{ not valid json`)
	if _, err := mcp.Load(p, ""); err == nil {
		t.Fatal("want an error for malformed json, got nil")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

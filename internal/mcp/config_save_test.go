// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package mcp_test

import (
	"path/filepath"
	"testing"

	"github.com/othershore-studio/llamingo/internal/mcp"
)

// SaveServers and LoadServers round-trip an http server (URL + auth header) and a
// stdio server (command), preserving each one's transport.
func TestSaveLoadServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp.json")
	want := map[string]mcp.ServerConfig{
		"news":  {Type: "http", URL: "https://news.example/mcp", Headers: map[string]string{"Authorization": "Bearer x"}},
		"local": {Command: "node", Args: []string{"server.js"}},
	}
	if err := mcp.SaveServers(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := mcp.LoadServers(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("servers = %d, want 2", len(got))
	}
	if got["news"].URL != "https://news.example/mcp" || got["news"].Headers["Authorization"] != "Bearer x" || got["news"].Transport() != "http" {
		t.Errorf("news round-trip = %+v", got["news"])
	}
	if got["local"].Command != "node" || got["local"].Transport() != "stdio" {
		t.Errorf("local round-trip = %+v", got["local"])
	}
}

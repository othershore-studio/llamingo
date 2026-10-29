// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"os"
	"path/filepath"
	"testing"
)

// The global MCP config is <root>/mcp.json, but a copied-in .mcp.json (Claude's
// per-project filename) is accepted too; mcp.json wins when both exist.
func TestGlobalMCPPath(t *testing.T) {
	dir := t.TempDir()
	if got := globalMCPPath(dir); got != filepath.Join(dir, "mcp.json") {
		t.Errorf("empty dir: got %q, want mcp.json", got)
	}

	dotted := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(dotted, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := globalMCPPath(dir); got != dotted {
		t.Errorf(".mcp.json fallback: got %q, want %q", got, dotted)
	}

	plain := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(plain, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := globalMCPPath(dir); got != plain {
		t.Errorf("mcp.json preferred over .mcp.json: got %q, want %q", got, plain)
	}
}

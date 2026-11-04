// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/taita"
)

// TestConfigIsCentralAndAdminGated proves the config/data split: server config
// lives in one shared store (so every session sees the same connections / Forge /
// MCP / settings), and only the admin may change it.
func TestConfigIsCentralAndAdminGated(t *testing.T) {
	srv := taita.New(backend.New("http://127.0.0.1:0"), t.TempDir(), nil,
		taita.WithConfigDir(t.TempDir()), taita.WithAdmin("dad"))
	t.Cleanup(func() { srv.Close() })

	// A regular user may not change server config.
	_, kid := do(t, srv, "POST", "/api/login", "", `{"name":"kid"}`)
	rec, kid := do(t, srv, "PUT", "/api/settings", kid, `{"forgeUrl":"http://forge.local"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("user PUT /api/settings = %d, want 403; body = %s", rec.Code, rec.Body)
	}

	// The admin may, writing to the central config store.
	_, dad := do(t, srv, "POST", "/api/login", "", `{"name":"dad","password":"pw"}`)
	rec, _ = do(t, srv, "PUT", "/api/settings", dad, `{"forgeUrl":"http://forge.local"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin PUT /api/settings = %d, want 204; body = %s", rec.Code, rec.Body)
	}

	// Central: the regular user's own session now sees the admin's setting.
	rec, _ = do(t, srv, "GET", "/api/settings", kid, "")
	var got struct {
		ForgeURL string `json:"forgeUrl"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode settings: %v; body = %s", err, rec.Body)
	}
	if got.ForgeURL != "http://forge.local" {
		t.Fatalf("central config not shared: user sees forgeUrl=%q, want http://forge.local", got.ForgeURL)
	}
}

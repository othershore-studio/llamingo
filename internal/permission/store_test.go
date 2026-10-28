// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package permission_test

import (
	"path/filepath"
	"testing"

	"github.com/othershore-studio/llamingo/internal/permission"
)

func TestFileGrantStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "grants.json")

	s := permission.NewFileGrantStore(path)
	if ok, err := s.Granted("tool:x"); err != nil || ok {
		t.Fatalf("empty store: %v, %v", ok, err)
	}
	if err := s.Grant("tool:x"); err != nil {
		t.Fatal(err)
	}

	// A fresh store at the same path sees the persisted grant.
	if ok, err := permission.NewFileGrantStore(path).Granted("tool:x"); err != nil || !ok {
		t.Fatalf("persisted grant not seen: %v, %v", ok, err)
	}

	// Revoking is also durable.
	if err := permission.NewFileGrantStore(path).Revoke("tool:x"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := permission.NewFileGrantStore(path).Granted("tool:x"); ok {
		t.Fatal("revoked grant still present on disk")
	}
}

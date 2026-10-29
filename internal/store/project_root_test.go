// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"path/filepath"
	"testing"
)

func TestProjectRoot(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// A free chat has no project folder.
	chat, err := s.NewChat("Free")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.ProjectRoot(chat.ID); err != nil || ok {
		t.Errorf("ProjectRoot(free chat) = ok %v, err %v; want ok=false", ok, err)
	}

	// A project session resolves to the project's working folder.
	work := filepath.Join(t.TempDir(), "myproject")
	p, err := s.OpenProject(work, "My Project")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.NewSession(p.ID, "Session")
	if err != nil {
		t.Fatal(err)
	}
	root, ok, err := s.ProjectRoot(sess.ID)
	if err != nil || !ok {
		t.Fatalf("ProjectRoot(session) = ok %v, err %v; want ok=true", ok, err)
	}
	if root != work {
		t.Errorf("ProjectRoot = %q, want %q", root, work)
	}

	// An unknown conversation is simply "no project", not an error.
	if _, ok, err := s.ProjectRoot("deadbeef"); err != nil || ok {
		t.Errorf("ProjectRoot(unknown) = ok %v, err %v; want ok=false, nil", ok, err)
	}
}

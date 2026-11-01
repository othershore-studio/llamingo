// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"path/filepath"
	"testing"
)

// The project settings default sensibly with no file, and each setter preserves
// the other's value (read-modify-write, not a clobbering full rewrite).
func TestProjectConfig(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "proj")

	// Defaults with no .llamingo/project.json: no disabled tools, note shown.
	if d, err := s.ProjectToolDefaults(root); err != nil || len(d) != 0 {
		t.Fatalf("ProjectToolDefaults default = %v, err %v; want empty", d, err)
	}
	if show, err := s.ProjectShowWorkingDir(root); err != nil || !show {
		t.Fatalf("ProjectShowWorkingDir default = %v, err %v; want true", show, err)
	}

	// Set the working-dir opt-out, then the tool defaults: the opt-out must survive.
	if err := s.SetProjectShowWorkingDir(root, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetProjectToolDefaults(root, []string{"write_file"}); err != nil {
		t.Fatal(err)
	}
	if show, err := s.ProjectShowWorkingDir(root); err != nil || show {
		t.Errorf("ProjectShowWorkingDir after setting tools = %v, err %v; want false (preserved)", show, err)
	}
	if d, err := s.ProjectToolDefaults(root); err != nil || len(d) != 1 || d[0] != "write_file" {
		t.Errorf("ProjectToolDefaults = %v, err %v; want [write_file]", d, err)
	}

	// Flip the note back on: the tool defaults must survive.
	if err := s.SetProjectShowWorkingDir(root, true); err != nil {
		t.Fatal(err)
	}
	if d, err := s.ProjectToolDefaults(root); err != nil || len(d) != 1 || d[0] != "write_file" {
		t.Errorf("ProjectToolDefaults after re-enabling note = %v, err %v; want [write_file] (preserved)", d, err)
	}
}

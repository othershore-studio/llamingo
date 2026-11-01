// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package filetools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// delete_file removes a file (gated, always-ask), refuses a folder, and a decline
// leaves the file in place.
func TestDeleteFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "gone.txt"), "bye")
	writeFile(t, filepath.Join(root, "keep.txt"), "stay")
	ctx := context.Background()

	tool := NewTool(root, gate(true), map[string]bool{})
	if _, err := tool.Call(ctx, toolDeleteFile, `{"path":"gone.txt"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gone.txt")); !os.IsNotExist(err) {
		t.Error("delete_file should have removed the file")
	}
	// Refuses a folder, pointing at delete_folder.
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(ctx, toolDeleteFile, `{"path":"dir"}`); err == nil {
		t.Error("delete_file on a folder should error")
	}
	// Escaping path rejected.
	if _, err := tool.Call(ctx, toolDeleteFile, `{"path":"../x"}`); err == nil {
		t.Error("delete_file should reject an escaping path")
	}

	// A declined delete keeps the file.
	denied := NewTool(root, gate(false), map[string]bool{})
	if out, err := denied.Call(ctx, toolDeleteFile, `{"path":"keep.txt"}`); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(strings.ToLower(out), "declin") {
		t.Errorf("declined delete = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "keep.txt")); err != nil {
		t.Error("a declined delete must leave the file in place")
	}
}

// delete_folder removes a folder and its contents, refuses a file, and never the
// project root.
func TestDeleteFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "sub", "a.txt"), "a")
	writeFile(t, filepath.Join(root, "sub", "nested", "b.txt"), "b")
	writeFile(t, filepath.Join(root, "plain.txt"), "x")
	ctx := context.Background()
	tool := NewTool(root, gate(true), map[string]bool{})

	if _, err := tool.Call(ctx, toolDeleteFolder, `{"path":"sub"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); !os.IsNotExist(err) {
		t.Error("delete_folder should have removed the folder and its contents")
	}
	// Refuses a file, pointing at delete_file.
	if _, err := tool.Call(ctx, toolDeleteFolder, `{"path":"plain.txt"}`); err == nil {
		t.Error("delete_folder on a file should error")
	}
	// Never the project root.
	if _, err := tool.Call(ctx, toolDeleteFolder, `{"path":"."}`); err == nil {
		t.Error("delete_folder on the project root should error")
	}
	if _, err := os.Stat(root); err != nil {
		t.Error("the project root must survive a refused root delete")
	}
}

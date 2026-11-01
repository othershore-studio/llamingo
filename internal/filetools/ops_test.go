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

// create_folder makes a folder (gated), is a no-op when it already exists, and is
// confined to the project.
func TestCreateFolder(t *testing.T) {
	root := t.TempDir()
	tool := NewTool(root, gate(true), map[string]bool{})
	ctx := context.Background()

	if _, err := tool.Call(ctx, toolCreateFolder, `{"path":"a/b/c"}`); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(root, "a", "b", "c")); err != nil || !info.IsDir() {
		t.Fatalf("create_folder did not make the folder: err=%v", err)
	}
	// Already there → a no-op, not an error.
	if out, err := tool.Call(ctx, toolCreateFolder, `{"path":"a/b/c"}`); err != nil {
		t.Fatalf("create_folder on an existing folder should not error: %v", err)
	} else if !strings.Contains(strings.ToLower(out), "already") {
		t.Errorf("create_folder on an existing folder = %q", out)
	}
	// A file in the way is an error.
	writeFile(t, filepath.Join(root, "file.txt"), "x")
	if _, err := tool.Call(ctx, toolCreateFolder, `{"path":"file.txt"}`); err == nil {
		t.Error("create_folder over an existing file should error")
	}
	// Escaping path rejected.
	if _, err := tool.Call(ctx, toolCreateFolder, `{"path":"../evil"}`); err == nil {
		t.Error("create_folder should reject an escaping path")
	}

	// A declined create makes nothing.
	denied := NewTool(root, gate(false), map[string]bool{})
	if out, err := denied.Call(ctx, toolCreateFolder, `{"path":"nope"}`); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(strings.ToLower(out), "declin") {
		t.Errorf("declined create = %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "nope")); !os.IsNotExist(err) {
		t.Error("a declined create must not make the folder")
	}
}

// rename_file moves a file, won't clobber an existing destination, refuses a
// folder (pointing at rename_folder), and carries the read-before-overwrite flag
// across the move.
func TestRenameFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "old.txt"), "body")
	writeFile(t, filepath.Join(root, "taken.txt"), "other")
	tool := NewTool(root, gate(true), map[string]bool{})
	ctx := context.Background()

	if _, err := tool.Call(ctx, toolRenameFile, `{"from":"old.txt","to":"sub/new.txt"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
		t.Error("rename_file should have moved the source away")
	}
	if b, err := os.ReadFile(filepath.Join(root, "sub", "new.txt")); err != nil || string(b) != "body" {
		t.Fatalf("rename_file destination wrong: %q err=%v", b, err)
	}

	// Won't overwrite an existing destination.
	writeFile(t, filepath.Join(root, "src.txt"), "src")
	if out, err := tool.Call(ctx, toolRenameFile, `{"from":"src.txt","to":"taken.txt"}`); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(strings.ToLower(out), "already exists") {
		t.Errorf("rename over an existing file = %q, want a refusal", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "taken.txt")); string(b) != "other" {
		t.Error("a refused rename must not change the destination")
	}

	// Refuses a folder, pointing at rename_folder.
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(ctx, toolRenameFile, `{"from":"dir","to":"dir2"}`); err == nil {
		t.Error("rename_file on a folder should error")
	}

	// Read flag carries across the rename: read, rename, then overwrite needs no re-read.
	wtool := NewTool(root, gate(true), map[string]bool{})
	if _, err := wtool.Call(ctx, toolReadFile, `{"path":"sub/new.txt"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := wtool.Call(ctx, toolRenameFile, `{"from":"sub/new.txt","to":"moved.txt"}`); err != nil {
		t.Fatal(err)
	}
	if out, err := wtool.Call(ctx, toolWriteFile, `{"path":"moved.txt","content":"rewritten"}`); err != nil {
		t.Fatal(err)
	} else if strings.Contains(strings.ToLower(out), "refused") {
		t.Errorf("overwrite after a read+rename should not be refused: %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "moved.txt")); string(b) != "rewritten" {
		t.Error("overwrite after read+rename should have taken")
	}
}

// rename_folder moves a folder, refuses a file (pointing at rename_file), and will
// not rename the project root.
func TestRenameFolder(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "pkg", "a.go"), "package pkg")
	writeFile(t, filepath.Join(root, "plain.txt"), "x")
	tool := NewTool(root, gate(true), map[string]bool{})
	ctx := context.Background()

	if _, err := tool.Call(ctx, toolRenameFolder, `{"from":"pkg","to":"lib"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "pkg")); !os.IsNotExist(err) {
		t.Error("rename_folder should have moved the source folder")
	}
	if b, err := os.ReadFile(filepath.Join(root, "lib", "a.go")); err != nil || string(b) != "package pkg" {
		t.Fatalf("rename_folder did not move the contents: %q err=%v", b, err)
	}

	// Refuses a file, pointing at rename_file.
	if _, err := tool.Call(ctx, toolRenameFolder, `{"from":"plain.txt","to":"other.txt"}`); err == nil {
		t.Error("rename_folder on a file should error")
	}
	// Won't rename the project root.
	if _, err := tool.Call(ctx, toolRenameFolder, `{"from":".","to":"elsewhere"}`); err == nil {
		t.Error("rename_folder on the project root should error")
	}
}

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

	"github.com/othershore-studio/llamingo/internal/permission"
)

type allowPrompter struct{ allow bool }

func (p allowPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: p.allow, Scope: permission.ScopeOnce}, nil
}

func gate(allow bool) *permission.Gate {
	return permission.New(allowPrompter{allow: allow}, permission.NewMemoryGrantStore())
}

// read_file returns a file's contents and is confined — a ".." escape is rejected.
func TestReadFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes.txt"), "hello world")
	tool := NewTool(root, nil, nil)

	out, err := tool.Call(context.Background(), toolReadFile, `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("read_file = %q, want the file contents", out)
	}
	if _, err := tool.Call(context.Background(), toolReadFile, `{"path":"../secret"}`); err == nil {
		t.Error("read_file should reject a path escaping the project")
	}
}

// file_properties reports a path's type, size, and modified time (read-only, no
// gate), for a file and a folder, and is confined.
func TestFileProperties(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "notes.txt"), "hello world") // 11 bytes
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewTool(root, nil, nil)

	out, err := tool.Call(context.Background(), toolFileProperties, `{"path":"notes.txt"}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"file", "11 bytes", "Modified:", "Mode:"} {
		if !strings.Contains(out, want) {
			t.Errorf("file_properties missing %q:\n%s", want, out)
		}
	}

	dirOut, err := tool.Call(context.Background(), toolFileProperties, `{"path":"dir"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dirOut, "folder") {
		t.Errorf("file_properties(dir) = %q, want it to say folder", dirOut)
	}

	if _, err := tool.Call(context.Background(), toolFileProperties, `{"path":"../etc"}`); err == nil {
		t.Error("file_properties should reject an escaping path")
	}
}

// write_file creates a new file without a prior read (and overwriting a file the
// tool itself just wrote is fine — it's seen), but a PRE-EXISTING file is refused
// until it's been read this chat. The gate approves the write.
func TestWriteFile(t *testing.T) {
	root := t.TempDir()
	tool := NewTool(root, gate(true), map[string]bool{})
	ctx := context.Background()

	// A new file: no read needed; creating it in a nested folder works.
	if _, err := tool.Call(ctx, toolWriteFile, `{"path":"out/result.txt","content":"first"}`); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "out", "result.txt")); err != nil || string(b) != "first" {
		t.Fatalf("new file not written: %q, err=%v", b, err)
	}
	// Overwriting the file the tool itself just wrote is fine (already seen).
	if _, err := tool.Call(ctx, toolWriteFile, `{"path":"out/result.txt","content":"second"}`); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "out", "result.txt")); string(b) != "second" {
		t.Error("overwrite of a tool-written file should take")
	}

	// A pre-existing file (on disk, never read via the tool) is refused until read.
	writeFile(t, filepath.Join(root, "existing.txt"), "original")
	res, err := tool.Call(ctx, toolWriteFile, `{"path":"existing.txt","content":"clobber"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(res), "read") {
		t.Errorf("overwrite without read = %q, want a read-first refusal", res)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "existing.txt")); string(b) != "original" {
		t.Error("the refused overwrite must not have changed the file")
	}

	// Read it, then the overwrite goes through.
	if _, err := tool.Call(ctx, toolReadFile, `{"path":"existing.txt"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(ctx, toolWriteFile, `{"path":"existing.txt","content":"clobber"}`); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "existing.txt")); string(b) != "clobber" {
		t.Error("overwrite after read should have taken")
	}
}

// A declined write leaves the file alone.
func TestWriteFileDenied(t *testing.T) {
	root := t.TempDir()
	tool := NewTool(root, gate(false), map[string]bool{})
	res, err := tool.Call(context.Background(), toolWriteFile, `{"path":"new.txt","content":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(res), "declin") {
		t.Errorf("denied write = %q, want a decline message", res)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Error("a declined write must not create the file")
	}
}

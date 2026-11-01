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

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")
	writeFile(t, filepath.Join(root, "README.md"), "# project")
	writeFile(t, filepath.Join(root, "internal", "a.go"), "package internal")
	tool := NewTool(root, nil, nil)

	// Root listing: the subdir sorts first (dirs before files), both files present.
	out, err := tool.Call(context.Background(), toolListFiles, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[dir]  internal") || !strings.Contains(out, "[file] main.go") || !strings.Contains(out, "README.md") {
		t.Fatalf("root listing missing entries:\n%s", out)
	}
	if i, j := strings.Index(out, "[dir]"), strings.Index(out, "[file]"); i < 0 || j < 0 || i > j {
		t.Errorf("directories should list before files:\n%s", out)
	}

	// Sub-path listing.
	out, err = tool.Call(context.Background(), toolListFiles, `{"path":"internal"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go") {
		t.Errorf("sub-path listing = %q, want a.go", out)
	}

	// Escapes are rejected.
	for _, bad := range []string{"..", "../secrets", `C:\Windows`, "/etc"} {
		if _, err := tool.Call(context.Background(), toolListFiles, `{"path":"`+bad+`"}`); err == nil {
			t.Errorf("list_files(path=%q) should be rejected as an escape", bad)
		}
	}
}

func TestListFilesLimit(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d", "e"} {
		writeFile(t, filepath.Join(root, n+".txt"), "x")
	}
	out, err := NewTool(root, nil, nil).Call(context.Background(), toolListFiles, `{"limit":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "5 entries, showing 2") {
		t.Errorf("limit not reflected in header:\n%s", out)
	}
	if n := strings.Count(out, "[file]"); n != 2 {
		t.Errorf("listed %d files, want 2 (the limit)", n)
	}
}

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

func TestSearchFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "main.go"), "package main")
	writeFile(t, filepath.Join(root, "internal", "client.go"), "package internal")
	writeFile(t, filepath.Join(root, "README.md"), "# x")
	writeFile(t, filepath.Join(root, "node_modules", "dep", "index.go"), "skip me")
	tool := NewTool(root, nil, nil)

	out, err := tool.Call(context.Background(), toolSearchFiles, `{"pattern":".go"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "internal/client.go") {
		t.Fatalf("search_files(.go) missing hits:\n%s", out)
	}
	if strings.Contains(out, "node_modules") {
		t.Errorf("search_files should skip node_modules:\n%s", out)
	}
	if strings.Contains(out, "README.md") {
		t.Errorf("search_files(.go) should not match README.md:\n%s", out)
	}

	if _, err := tool.Call(context.Background(), toolSearchFiles, `{}`); err == nil {
		t.Error("search_files with no pattern should error")
	}
}

func TestSearchInFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "a.go"), "package main\n// TODO: fix this\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "b.txt"), "nothing to see here\n")
	// A binary file that contains the needle in its bytes must be skipped.
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte("TODO\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewTool(root, nil, nil)

	out, err := tool.Call(context.Background(), toolSearchInFiles, `{"pattern":"todo"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a.go:2:") || !strings.Contains(out, "TODO: fix this") {
		t.Fatalf("search_in_files(todo) missing the match:\n%s", out)
	}
	if strings.Contains(out, "bin.dat") {
		t.Errorf("search_in_files should skip binary files:\n%s", out)
	}
	if strings.Contains(out, "b.txt") {
		t.Errorf("search_in_files(todo) should not match b.txt:\n%s", out)
	}

	// An escaping path is rejected.
	if _, err := tool.Call(context.Background(), toolSearchInFiles, `{"pattern":"x","path":"../"}`); err == nil {
		t.Error("search_in_files with an escaping path should error")
	}
}

func TestFolderTree(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "cmd", "app", "main.go"), "package main")
	writeFile(t, filepath.Join(root, "internal", "client.go"), "package internal")
	writeFile(t, filepath.Join(root, "README.md"), "# x")
	writeFile(t, filepath.Join(root, ".git", "config"), "[core]")
	writeFile(t, filepath.Join(root, "node_modules", "dep", "index.js"), "skip me")
	tool := NewTool(root, nil, nil)

	out, err := tool.Call(context.Background(), toolFolderTree, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	// Folders carry a trailing slash; nested files appear indented under them.
	for _, want := range []string{"cmd/", "  app/", "    main.go", "internal/", "  client.go", "README.md"} {
		if !strings.Contains(out, want) {
			t.Errorf("tree missing %q:\n%s", want, out)
		}
	}
	// .git and node_modules are pruned.
	if strings.Contains(out, ".git") || strings.Contains(out, "node_modules") || strings.Contains(out, "index.js") {
		t.Errorf("tree should skip .git and node_modules:\n%s", out)
	}

	// depth=1 shows only direct children, not their contents.
	shallow, err := tool.Call(context.Background(), toolFolderTree, `{"depth":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shallow, "cmd/") {
		t.Errorf("depth=1 tree should show cmd/:\n%s", shallow)
	}
	if strings.Contains(shallow, "main.go") || strings.Contains(shallow, "app/") {
		t.Errorf("depth=1 tree should not descend into cmd/:\n%s", shallow)
	}

	// A sub-path roots the tree there.
	sub, err := tool.Call(context.Background(), toolFolderTree, `{"path":"cmd"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sub, "app/") || !strings.Contains(sub, "main.go") {
		t.Errorf("tree of cmd/ should show app/main.go:\n%s", sub)
	}
	if strings.Contains(sub, "internal") {
		t.Errorf("tree of cmd/ should not show siblings:\n%s", sub)
	}

	// An escaping path is rejected.
	if _, err := tool.Call(context.Background(), toolFolderTree, `{"path":"../"}`); err == nil {
		t.Error("folder_tree with an escaping path should error")
	}
}

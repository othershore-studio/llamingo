// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package filetools holds the project file tools: listing, finding files by
// name, and searching their contents. Like memtool and localtools, the bundle
// satisfies the MCP loop's Registry. Every tool is confined to one project
// folder (the root passed to NewTool) — a path argument that would escape it, by
// "..", an absolute path, or a symlink, is rejected — so these are offered only
// in a project session. Reads only; no permission gate. Results are capped so a
// large tree never floods the model's context.
package filetools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
)

const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

// Tool is the bundle of project file tools, confined to root.
type Tool struct {
	root string
}

// NewTool returns the file tools confined to the given project folder.
func NewTool(projectRoot string) *Tool { return &Tool{root: projectRoot} }

const (
	toolListFiles     = "list_files"
	toolSearchFiles   = "search_files"
	toolSearchInFiles = "search_in_files"
)

// Tools advertises the file tools to the model.
func (t *Tool) Tools() []backend.ToolDef {
	return []backend.ToolDef{
		{
			Name: toolListFiles,
			Description: "List the files and folders in the project, at an optional sub-path " +
				"(defaults to the project root). Use it to see what is in the project before reading or searching.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"sub-path within the project; omit for the root"},` +
				`"limit":{"type":"integer","description":"max entries to return (default 200)"}}}`),
		},
		{
			Name: toolSearchFiles,
			Description: "Find files whose name contains a substring, recursively under an optional " +
				"sub-path (defaults to the project root). Case-insensitive; skips .git and node_modules.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"pattern":{"type":"string","description":"substring to match in the file name"},` +
				`"path":{"type":"string","description":"sub-path to search under; omit for the root"},` +
				`"limit":{"type":"integer","description":"max results (default 100)"}},"required":["pattern"]}`),
		},
		{
			Name: toolSearchInFiles,
			Description: "Search file contents for a substring, like grep, recursively under an optional " +
				"sub-path (defaults to the project root). Case-insensitive; returns path:line: text. Skips " +
				"binary files, .git, and node_modules.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"pattern":{"type":"string","description":"substring to find in file contents"},` +
				`"path":{"type":"string","description":"sub-path to search under; omit for the root"},` +
				`"limit":{"type":"integer","description":"max matching lines (default 50)"}},"required":["pattern"]}`),
		},
	}
}

// Call dispatches a file tool by name.
func (t *Tool) Call(ctx context.Context, name, arguments string) (string, error) {
	switch name {
	case toolListFiles:
		return t.listFiles(arguments)
	case toolSearchFiles:
		return t.searchFiles(arguments)
	case toolSearchInFiles:
		return t.searchInFiles(arguments)
	default:
		return "", fmt.Errorf("filetools: unknown tool %q", name)
	}
}

func (t *Tool) listFiles(arguments string) (string, error) {
	var a struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if strings.TrimSpace(arguments) != "" {
		if err := json.Unmarshal([]byte(arguments), &a); err != nil {
			return "", fmt.Errorf("filetools: arguments: %w", err)
		}
	}
	limit := clampLimit(a.Limit, defaultListLimit, maxListLimit)

	dir, err := resolveWithin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("filetools: list %s: %w", displayPath(a.Path), err)
	}
	// Directories first, then by name.
	sort.Slice(entries, func(i, j int) bool {
		if di, dj := entries[i].IsDir(), entries[j].IsDir(); di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	var b strings.Builder
	shown := 0
	for _, e := range entries {
		if shown >= limit {
			break
		}
		if e.IsDir() {
			fmt.Fprintf(&b, "[dir]  %s\n", e.Name())
		} else {
			var size int64
			if info, err := e.Info(); err == nil {
				size = info.Size()
			}
			fmt.Fprintf(&b, "[file] %s (%s)\n", e.Name(), humanSize(size))
		}
		shown++
	}

	header := fmt.Sprintf("Listing of %s — %d entries", displayPath(a.Path), len(entries))
	if len(entries) > shown {
		header += fmt.Sprintf(", showing %d (raise limit or narrow the path for more)", shown)
	}
	return header + ":\n" + b.String(), nil
}

// resolveWithin resolves rel against root and returns an absolute path confined
// to root, rejecting an absolute rel, a ".." escape, and a symlink that leaves
// root. rel "" or "." means root itself.
func resolveWithin(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("filetools: path must be relative to the project folder")
	}
	root = filepath.Clean(root)
	joined := filepath.Join(root, filepath.FromSlash(rel))
	if escapes(root, joined) {
		return "", fmt.Errorf("filetools: path %q escapes the project folder", rel)
	}
	// Follow symlinks where the target exists, and re-confirm confinement.
	rootReal, err1 := filepath.EvalSymlinks(root)
	joinedReal, err2 := filepath.EvalSymlinks(joined)
	if err1 == nil && err2 == nil && escapes(rootReal, joinedReal) {
		return "", fmt.Errorf("filetools: path %q escapes the project folder", rel)
	}
	return joined, nil
}

// escapes reports whether p lies outside base.
func escapes(base, p string) bool {
	rel, err := filepath.Rel(base, p)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// displayPath names a sub-path for messages: the root, or the quoted sub-path.
func displayPath(rel string) string {
	if strings.TrimSpace(rel) == "" || rel == "." {
		return "the project root"
	}
	return fmt.Sprintf("%q", rel)
}

func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package filetools holds the project file tools: listing, finding files by
// name, searching their contents, reading a file, and writing one. Like memtool
// and localtools, the bundle satisfies the MCP loop's Registry. Every tool is
// confined to one project folder (the root passed to NewTool) — a path argument
// that would escape it, by "..", an absolute path, or a symlink, is rejected — so
// these are offered only in a project session. Reads are ungated; write_file is
// gated (the user approves each write) and may overwrite an existing file only
// after it has been read this chat. Results are capped so a large tree or file
// never floods the model's context.
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
	"github.com/othershore-studio/llamingo/internal/permission"
)

const (
	defaultListLimit = 200
	maxListLimit     = 1000
)

// Tool is the bundle of project file tools, confined to root.
type Tool struct {
	root  string
	gate  *permission.Gate // write_file's consent prompt (nil when only listing tools)
	reads map[string]bool  // files read this chat (confined abs paths) — write_file's read-before-overwrite guard
}

// NewTool returns the file tools confined to the given project folder. gate
// mediates write_file (nil is fine when the tools are only being listed); reads
// is the per-conversation set of files read so far (nil starts an empty one).
func NewTool(projectRoot string, gate *permission.Gate, reads map[string]bool) *Tool {
	if reads == nil {
		reads = map[string]bool{}
	}
	return &Tool{root: projectRoot, gate: gate, reads: reads}
}

const (
	toolListFiles     = "list_files"
	toolSearchFiles   = "search_files"
	toolSearchInFiles = "search_in_files"
	toolFolderTree    = "folder_tree"
	toolReadFile      = "read_file"
	toolWriteFile     = "write_file"
	toolRenameFile    = "rename_file"
	toolCreateFolder  = "create_folder"
	toolRenameFolder  = "rename_folder"
	toolDeleteFile    = "delete_file"
	toolDeleteFolder  = "delete_folder"
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
		{
			Name: toolFolderTree,
			Description: "Show the project's folder tree — files and folders nested — under an optional " +
				"sub-path, to a depth (default 3). Capped; skips .git and node_modules. Good for seeing the " +
				"project layout at a glance before listing or reading.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"sub-path to root the tree at; omit for the project root"},` +
				`"depth":{"type":"integer","description":"how many levels deep (default 3)"},` +
				`"limit":{"type":"integer","description":"max entries (default 300)"}}}`),
		},
		{
			Name: toolReadFile,
			Description: "Read a file's contents in the project, by its path (relative to the project " +
				"root). Returns the text, capped for very large files. Read a file before you overwrite it.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"the file's path within the project"}},"required":["path"]}`),
		},
		{
			Name: toolWriteFile,
			Description: "Write a file in the project — create a new one, or overwrite an existing one " +
				"with the given content (a full-file write, not a patch). You must read_file an existing file " +
				"first, so you don't clobber content you haven't seen; a new file needs no read. Asks the user " +
				"to approve the write.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"the file's path within the project"},` +
				`"content":{"type":"string","description":"the full new contents of the file"}},` +
				`"required":["path","content"]}`),
		},
		{
			Name: toolRenameFile,
			Description: "Rename or move a file within the project, from one path to another (both " +
				"relative to the project root). The source must be a file that exists; the destination must " +
				"not already exist. Asks the user to approve.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"from":{"type":"string","description":"the file's current path within the project"},` +
				`"to":{"type":"string","description":"the new path within the project"}},` +
				`"required":["from","to"]}`),
		},
		{
			Name: toolCreateFolder,
			Description: "Create a folder in the project at the given path (relative to the project " +
				"root); missing parent folders are created too. Asks the user to approve.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"the folder's path within the project"}},` +
				`"required":["path"]}`),
		},
		{
			Name: toolRenameFolder,
			Description: "Rename or move a folder within the project, from one path to another (both " +
				"relative to the project root). The source must be a folder that exists; the destination must " +
				"not already exist. Asks the user to approve.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"from":{"type":"string","description":"the folder's current path within the project"},` +
				`"to":{"type":"string","description":"the new path within the project"}},` +
				`"required":["from","to"]}`),
		},
		{
			Name: toolDeleteFile,
			Description: "Delete a file in the project, by its path (relative to the project root). " +
				"Permanent — asks the user to confirm every time (it can't be undone).",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"the file's path within the project"}},` +
				`"required":["path"]}`),
		},
		{
			Name: toolDeleteFolder,
			Description: "Delete a folder in the project and everything inside it, by its path " +
				"(relative to the project root). Permanent and recursive — asks the user to confirm every " +
				"time, and cannot delete the project root.",
			Parameters: json.RawMessage(`{"type":"object","properties":{` +
				`"path":{"type":"string","description":"the folder's path within the project"}},` +
				`"required":["path"]}`),
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
	case toolFolderTree:
		return t.folderTree(arguments)
	case toolReadFile:
		return t.readFile(arguments)
	case toolWriteFile:
		return t.writeFile(ctx, arguments)
	case toolRenameFile:
		return t.renameWithin(ctx, toolRenameFile, arguments, false)
	case toolCreateFolder:
		return t.createFolder(ctx, arguments)
	case toolRenameFolder:
		return t.renameWithin(ctx, toolRenameFolder, arguments, true)
	case toolDeleteFile:
		return t.deleteFile(ctx, arguments)
	case toolDeleteFolder:
		return t.deleteFolder(ctx, arguments)
	default:
		return "", fmt.Errorf("filetools: unknown tool %q", name)
	}
}

// readFile returns a file's contents (capped) and records that it was read, so a
// later write_file may overwrite it.
func (t *Tool) readFile(arguments string) (string, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", fmt.Errorf("filetools: arguments: %w", err)
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", fmt.Errorf("filetools: path is required")
	}
	abs, err := resolveWithin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("filetools: read %s: %w", displayPath(a.Path), err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("filetools: %s is a folder — use list_files", displayPath(a.Path))
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("filetools: read %s: %w", displayPath(a.Path), err)
	}
	t.reads[abs] = true
	const maxBytes = 128 * 1024
	out, truncated := string(data), false
	if len(out) > maxBytes {
		out, truncated = out[:maxBytes], true
	}
	header := fmt.Sprintf("%s (%s)", displayPath(a.Path), humanSize(info.Size()))
	if truncated {
		header += fmt.Sprintf(" — first %s", humanSize(maxBytes))
	}
	return header + ":\n" + out, nil
}

// writeFile creates or overwrites a file. An existing file must have been read
// this chat first (read-before-overwrite); a new one need not be. Either way the
// user approves the write through the gate.
func (t *Tool) writeFile(ctx context.Context, arguments string) (string, error) {
	if t.gate == nil {
		return "", fmt.Errorf("filetools: write_file is unavailable here")
	}
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", fmt.Errorf("filetools: arguments: %w", err)
	}
	if strings.TrimSpace(a.Path) == "" {
		return "", fmt.Errorf("filetools: path is required")
	}
	abs, err := resolveWithin(t.root, a.Path)
	if err != nil {
		return "", err
	}
	info, statErr := os.Stat(abs)
	exists := statErr == nil
	if exists && info.IsDir() {
		return "", fmt.Errorf("filetools: %s is a folder", displayPath(a.Path))
	}
	// Read-before-overwrite: a file the model hasn't seen can't be clobbered by a
	// full-file write. A new file is exempt.
	if exists && !t.reads[abs] {
		return fmt.Sprintf("Refused: read %s with read_file before overwriting it (so unseen content isn't lost), then write again.", displayPath(a.Path)), nil
	}
	// Confine the parent too (resolveWithin skips the symlink check for a path that
	// does not exist yet), so a symlinked folder can't lead a new file out of root.
	if real, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		if rootReal, err := filepath.EvalSymlinks(filepath.Clean(t.root)); err == nil && escapes(rootReal, real) {
			return "", fmt.Errorf("filetools: %s is outside the project folder", displayPath(a.Path))
		}
	}

	verb := "create"
	if exists {
		verb = "overwrite"
	}
	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + toolWriteFile,
		Label:  verb + " " + displayPath(a.Path),
		Detail: a.Content,
		Risk:   permission.RiskMedium,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return fmt.Sprintf("Not written: you declined to %s %s. Do not tell the user it was saved.", verb, displayPath(a.Path)), nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("filetools: write %s: %w", displayPath(a.Path), err)
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil {
		return "", fmt.Errorf("filetools: write %s: %w", displayPath(a.Path), err)
	}
	t.reads[abs] = true // now seen → further overwrites this chat are fine
	return fmt.Sprintf("Wrote %s (%d bytes).", displayPath(a.Path), len(a.Content)), nil
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

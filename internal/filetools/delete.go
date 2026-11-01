// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package filetools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/othershore-studio/llamingo/internal/permission"
)

// The destructive operations. A delete can't be undone, so these are RiskHigh:
// the gate offers only "once", and the user reaffirms every single call — there
// is no session grant to breeze past.

// deleteFile removes a single file from the project.
func (t *Tool) deleteFile(ctx context.Context, arguments string) (string, error) {
	if t.gate == nil {
		return "", fmt.Errorf("filetools: delete_file is unavailable here")
	}
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
		return "", fmt.Errorf("filetools: delete %s: %w", displayPath(a.Path), err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("filetools: %s is a folder — use delete_folder", displayPath(a.Path))
	}

	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + toolDeleteFile,
		Label:  "delete " + displayPath(a.Path),
		Detail: fmt.Sprintf("Permanently deletes this file (%s). This can't be undone.", humanSize(info.Size())),
		Risk:   permission.RiskHigh,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return fmt.Sprintf("Not deleted: you declined to delete %s. Do not tell the user it was removed.", displayPath(a.Path)), nil
	}
	if err := os.Remove(abs); err != nil {
		return "", fmt.Errorf("filetools: delete %s: %w", displayPath(a.Path), err)
	}
	delete(t.reads, abs)
	return fmt.Sprintf("Deleted %s.", displayPath(a.Path)), nil
}

// deleteFolder removes a folder and everything inside it, recursively. Never the
// project root.
func (t *Tool) deleteFolder(ctx context.Context, arguments string) (string, error) {
	if t.gate == nil {
		return "", fmt.Errorf("filetools: delete_folder is unavailable here")
	}
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
	if abs == filepath.Clean(t.root) {
		return "", fmt.Errorf("filetools: cannot delete the project root")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("filetools: delete %s: %w", displayPath(a.Path), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("filetools: %s is a file — use delete_file", displayPath(a.Path))
	}

	n, capped := countUnder(abs, 500)
	count := fmt.Sprintf("%d", n)
	if capped {
		count = fmt.Sprintf("%d+", n)
	}
	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + toolDeleteFolder,
		Label:  fmt.Sprintf("delete folder %s and everything in it", displayPath(a.Path)),
		Detail: fmt.Sprintf("Permanently deletes this folder and all %s items inside it. This can't be undone.", count),
		Risk:   permission.RiskHigh,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return fmt.Sprintf("Not deleted: you declined to delete %s. Do not tell the user it was removed.", displayPath(a.Path)), nil
	}
	if err := os.RemoveAll(abs); err != nil {
		return "", fmt.Errorf("filetools: delete %s: %w", displayPath(a.Path), err)
	}
	// Drop any read flags for files under the folder that's now gone.
	prefix := abs + string(filepath.Separator)
	for k := range t.reads {
		if k == abs || strings.HasPrefix(k, prefix) {
			delete(t.reads, k)
		}
	}
	return fmt.Sprintf("Deleted folder %s.", displayPath(a.Path)), nil
}

// countUnder counts entries (files and folders) beneath abs, stopping once it
// reaches limit; the second return is true when the limit was hit (more remain).
// It feeds the delete prompt an honest sense of scale without walking a huge tree.
func countUnder(abs string, limit int) (int, bool) {
	n, capped := 0, false
	_ = filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == abs {
			return nil
		}
		n++
		if n >= limit {
			capped = true
			return filepath.SkipAll
		}
		return nil
	})
	return n, capped
}

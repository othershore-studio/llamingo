// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package filetools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/othershore-studio/llamingo/internal/permission"
)

// The file-service operations that change the tree: renaming, creating folders,
// and (in delete.go) removing things. Each is confined to the project root like
// the read tools, and each asks the user through the gate before it acts.

// renameWithin moves fromPath to toPath within the project. wantDir selects
// whether the source must be a folder (rename_folder) or a file (rename_file),
// so each tool refuses the other kind with a pointer to its sibling. An existing
// destination is never clobbered. RiskMedium: the user approves, once or for the
// session.
func (t *Tool) renameWithin(ctx context.Context, toolName, arguments string, wantDir bool) (string, error) {
	if t.gate == nil {
		return "", fmt.Errorf("filetools: %s is unavailable here", toolName)
	}
	var a struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal([]byte(arguments), &a); err != nil {
		return "", fmt.Errorf("filetools: arguments: %w", err)
	}
	if strings.TrimSpace(a.From) == "" || strings.TrimSpace(a.To) == "" {
		return "", fmt.Errorf("filetools: %s needs both from and to", toolName)
	}
	fromAbs, err := resolveWithin(t.root, a.From)
	if err != nil {
		return "", err
	}
	toAbs, err := resolveWithin(t.root, a.To)
	if err != nil {
		return "", err
	}

	kind := "file"
	if wantDir {
		kind = "folder"
	}
	info, err := os.Stat(fromAbs)
	if err != nil {
		return "", fmt.Errorf("filetools: rename %s: %w", displayPath(a.From), err)
	}
	if info.IsDir() != wantDir {
		sibling := "rename_folder"
		is := "a folder"
		if wantDir {
			sibling, is = "rename_file", "a file"
		}
		return "", fmt.Errorf("filetools: %s is %s — use %s", displayPath(a.From), is, sibling)
	}
	if wantDir && fromAbs == filepath.Clean(t.root) {
		return "", fmt.Errorf("filetools: cannot rename the project root")
	}
	if _, err := os.Stat(toAbs); err == nil {
		return fmt.Sprintf("Refused: %s already exists — pick a destination that doesn't, or remove it first.", displayPath(a.To)), nil
	}
	if err := confineParent(t.root, toAbs); err != nil {
		return "", err
	}

	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + toolName,
		Label:  fmt.Sprintf("rename %s %s → %s", kind, displayPath(a.From), displayPath(a.To)),
		Risk:   permission.RiskMedium,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return fmt.Sprintf("Not renamed: you declined to rename %s. Do not tell the user it was moved.", displayPath(a.From)), nil
	}
	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return "", fmt.Errorf("filetools: rename %s: %w", displayPath(a.From), err)
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return "", fmt.Errorf("filetools: rename %s: %w", displayPath(a.From), err)
	}
	// Carry the read-before-overwrite flag across a file rename, so a write to the
	// new name needn't re-read what was already seen under the old one.
	if !wantDir && t.reads[fromAbs] {
		delete(t.reads, fromAbs)
		t.reads[toAbs] = true
	}
	return fmt.Sprintf("Renamed %s to %s.", displayPath(a.From), displayPath(a.To)), nil
}

// createFolder makes a folder (and any missing parents) within the project.
// Already-present folders are a no-op; a file in the way is an error. RiskMedium.
func (t *Tool) createFolder(ctx context.Context, arguments string) (string, error) {
	if t.gate == nil {
		return "", fmt.Errorf("filetools: create_folder is unavailable here")
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
	if info, err := os.Stat(abs); err == nil {
		if info.IsDir() {
			return fmt.Sprintf("%s already exists.", displayPath(a.Path)), nil
		}
		return "", fmt.Errorf("filetools: %s already exists as a file", displayPath(a.Path))
	}
	if err := confineParent(t.root, abs); err != nil {
		return "", err
	}

	allow, err := t.gate.Allow(ctx, permission.Request{
		ToolID: "native:" + toolCreateFolder,
		Label:  "create folder " + displayPath(a.Path),
		Risk:   permission.RiskMedium,
	})
	if err != nil {
		return "", err
	}
	if !allow {
		return fmt.Sprintf("Not created: you declined to create %s.", displayPath(a.Path)), nil
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return "", fmt.Errorf("filetools: create folder %s: %w", displayPath(a.Path), err)
	}
	return fmt.Sprintf("Created folder %s.", displayPath(a.Path)), nil
}

// confineParent re-confirms that abs's parent directory stays within root once
// symlinks are resolved. resolveWithin skips the symlink check for a path that
// doesn't exist yet (a new file or folder), so a symlinked parent could otherwise
// lead the new entry out of the project; this closes that gap.
func confineParent(root, abs string) error {
	real, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil // parent doesn't resolve yet; the lexical Join-confinement already held
	}
	rootReal, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return nil
	}
	if escapes(rootReal, real) {
		return fmt.Errorf("filetools: that path is outside the project folder")
	}
	return nil
}

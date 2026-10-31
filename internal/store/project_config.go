// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// projectConfig is llaminGO's per-project settings, stored in a .llamingo/ folder
// in the project's working directory (mirroring Claude's .claude/). It travels with
// the repo, so a project's tool setup is portable. Kept minimal for now.
type projectConfig struct {
	DisabledTools []string `json:"disabledTools,omitempty"` // system tools a new session in this project starts with off
}

// projectConfigPath is a project's llaminGO config file, in its working folder.
func projectConfigPath(root string) string {
	return filepath.Join(root, ".llamingo", "project.json")
}

// ProjectToolDefaults returns the tools a new session in this project starts with
// disabled (empty when the project has no .llamingo/project.json). A new session
// inherits these; it can still override them per chat.
func (s *Store) ProjectToolDefaults(root string) ([]string, error) {
	var c projectConfig
	err := readJSON(projectConfigPath(root), &c)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: read project config: %w", err)
	}
	return c.DisabledTools, nil
}

// SetProjectToolDefaults writes the project's default-disabled tool set to its
// .llamingo/project.json, creating the .llamingo/ folder on first use.
func (s *Store) SetProjectToolDefaults(root string, disabled []string) error {
	if err := os.MkdirAll(filepath.Join(root, ".llamingo"), 0o755); err != nil {
		return fmt.Errorf("store: create .llamingo: %w", err)
	}
	return writeJSON(projectConfigPath(root), projectConfig{DisabledTools: disabled})
}

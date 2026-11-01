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
	DisabledTools  []string `json:"disabledTools,omitempty"`  // system tools a new session in this project starts with off
	HideWorkingDir bool     `json:"hideWorkingDir,omitempty"` // suppress the "your working directory is …" prompt note (on by default)
}

// projectConfigPath is a project's llaminGO config file, in its working folder.
func projectConfigPath(root string) string {
	return filepath.Join(root, ".llamingo", "project.json")
}

// loadProjectConfig reads a project's config, returning the zero value (not an
// error) when it has no .llamingo/project.json yet.
func loadProjectConfig(root string) (projectConfig, error) {
	var c projectConfig
	err := readJSON(projectConfigPath(root), &c)
	if errors.Is(err, fs.ErrNotExist) {
		return projectConfig{}, nil
	}
	if err != nil {
		return projectConfig{}, fmt.Errorf("store: read project config: %w", err)
	}
	return c, nil
}

// saveProjectConfig writes the whole config back, creating .llamingo/ on first use.
func saveProjectConfig(root string, c projectConfig) error {
	if err := os.MkdirAll(filepath.Join(root, ".llamingo"), 0o755); err != nil {
		return fmt.Errorf("store: create .llamingo: %w", err)
	}
	return writeJSON(projectConfigPath(root), c)
}

// ProjectToolDefaults returns the tools a new session in this project starts with
// disabled (empty when the project has no .llamingo/project.json). A new session
// inherits these; it can still override them per chat.
func (s *Store) ProjectToolDefaults(root string) ([]string, error) {
	c, err := loadProjectConfig(root)
	if err != nil {
		return nil, err
	}
	return c.DisabledTools, nil
}

// SetProjectToolDefaults writes the project's default-disabled tool set, leaving
// the project's other settings intact.
func (s *Store) SetProjectToolDefaults(root string, disabled []string) error {
	c, err := loadProjectConfig(root)
	if err != nil {
		return err
	}
	c.DisabledTools = disabled
	return saveProjectConfig(root, c)
}

// ProjectShowWorkingDir reports whether a project session prepends the "your
// working directory is …" note to the model's prompt. On by default; a project
// opts out with hideWorkingDir in its .llamingo/project.json.
func (s *Store) ProjectShowWorkingDir(root string) (bool, error) {
	c, err := loadProjectConfig(root)
	if err != nil {
		return true, err
	}
	return !c.HideWorkingDir, nil
}

// SetProjectShowWorkingDir sets whether the working-directory note is shown,
// leaving the project's other settings intact.
func (s *Store) SetProjectShowWorkingDir(root string, show bool) error {
	c, err := loadProjectConfig(root)
	if err != nil {
		return err
	}
	c.HideWorkingDir = !show
	return saveProjectConfig(root, c)
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Prefs is per-user preferences — personal, not server policy. It lives in a
// store-local prefs.json: on the desktop beside config.json for the single user;
// on a taita server each user has their own, kept out of the admin's central
// config. See docs/taita.md (config/data split) — Prefs is the user half.
type Prefs struct {
	// Location is the user's place (city/country) for place-aware tools; it
	// overrides any server default (Config.Location).
	Location string `json:"location,omitempty"`
	// SystemPrompt is the user's standing instructions, composed after the active
	// connection's persona prompt on every turn (empty = none).
	SystemPrompt string `json:"systemPrompt,omitempty"`
}

func (s *Store) prefsFile() string { return filepath.Join(s.root, "prefs.json") }

// Prefs returns the per-user preferences. A missing file is the zero Prefs, not an
// error.
func (s *Store) Prefs() (Prefs, error) {
	b, err := os.ReadFile(s.prefsFile())
	if errors.Is(err, fs.ErrNotExist) {
		return Prefs{}, nil
	}
	if err != nil {
		return Prefs{}, fmt.Errorf("store: read prefs: %w", err)
	}
	var p Prefs
	if err := json.Unmarshal(b, &p); err != nil {
		return Prefs{}, fmt.Errorf("store: parse prefs: %w", err)
	}
	return p, nil
}

// SetPrefs persists the per-user preferences.
func (s *Store) SetPrefs(p Prefs) error { return writeJSON(s.prefsFile(), p) }

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

func (s *Store) configFile() string { return filepath.Join(s.root, "config.json") }

// Root is the store's base directory (e.g. ~/.llamingo), where app-wide config
// such as mcp.json lives next to the conversation roots.
func (s *Store) Root() string { return s.root }

// Config is the persisted app configuration — the llama-server connection, the
// user's location, and the Forge image server. It lives at <root>/config.json
// next to the memory file, so these survive across launches without re-exporting
// an env var.
type Config struct {
	URL      string `json:"url,omitempty"`
	Token    string `json:"token,omitempty"`
	Location string `json:"location,omitempty"` // the user's manually set location (city/country)
	ForgeURL string `json:"forgeUrl,omitempty"` // Forge Neo image server (empty = image generation off)
	Debug    bool   `json:"debug,omitempty"`    // write raw llama/Forge/tool payloads to each chat's logs/ (debug mode)

	// Connections are the saved llama-servers the user switches between; URL/Token
	// above is whichever one is active now. See the Connection type.
	Connections []Connection `json:"connections,omitempty"`
}

// Connection is a saved llama-server the user can switch between — a name plus its
// URL and optional bearer token. One server serves one model, so switching the
// connection is how the user switches model; the active one is Config.URL/Token.
type Connection struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

// Config returns the saved configuration. A missing file is the zero Config, not
// an error.
func (s *Store) Config() (Config, error) {
	b, err := os.ReadFile(s.configFile())
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("store: read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("store: parse config: %w", err)
	}
	return c, nil
}

// SetConfig persists the configuration.
func (s *Store) SetConfig(c Config) error {
	return writeJSON(s.configFile(), c)
}

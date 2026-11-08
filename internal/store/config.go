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
	TaitaURL string `json:"taitaUrl,omitempty"` // a remote taita to surface as this client's Server Chats (P3); empty = local-only
	DNS      string `json:"dns,omitempty"`      // DNS server for name resolution (host or host:port); mainly Android/Termux, where Go's resolver finds no nameserver. Empty = the OS resolver.
	Debug    bool   `json:"debug,omitempty"`    // write raw llama/Forge/tool payloads to each chat's logs/ (debug mode)

	// FetchMaxChars caps how many characters of fetched page text the fetch tool
	// hands the model (0 = the ~48K default; negative = no limit). The big result
	// lives in context for only the one post-fetch round, so this protects a small
	// context from that spike; raise it (or set it negative) for a large-context model.
	FetchMaxChars int `json:"fetchMaxChars,omitempty"`

	// UserAgent is the User-Agent the fetch tool sends (empty = the honest default
	// that names llaminGO). A user may set a browser UA for a site that requires one.
	UserAgent string `json:"userAgent,omitempty"`

	// ActiveID is which saved Connection is active (stable across url/name edits),
	// so two connections on the same url are distinguishable. URL/Token and
	// Temperature/MinP below mirror the active connection for the backend + startup.
	ActiveID string `json:"activeConnectionId,omitempty"`

	// Temperature and MinP are the ACTIVE connection's sampling, applied to every
	// turn (nil = the server/model default). Set when a connection is activated.
	Temperature *float64 `json:"temperature,omitempty"`
	MinP        *float64 `json:"minP,omitempty"`
	// SystemPrompt is the ACTIVE connection's system prompt, prepended to every turn
	// (empty = none). Set when a connection is activated; read fresh per turn.
	SystemPrompt string `json:"systemPrompt,omitempty"`

	// Connections are the saved llama-servers the user switches between; URL/Token
	// (and Temperature/MinP/SystemPrompt) above are whichever one is active now.
	Connections []Connection `json:"connections,omitempty"`
}

// Connection is a saved llama-server the user can switch between — a name plus its
// URL and optional bearer token. One server serves one model, so switching the
// connection is how the user switches model; the active one is Config.URL/Token.
type Connection struct {
	ID    string `json:"id"` // stable, survives url/name edits — how the active one is tracked
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
	// Sampling for this connection (nil = the server/model default). This is what
	// lets one saved server double as personas — e.g. a low-temperature "Coder" and
	// a high-temperature "Creative" both pointing at the same URL.
	Temperature *float64 `json:"temperature,omitempty"`
	MinP        *float64 `json:"minP,omitempty"`
	// SystemPrompt is this connection's own instructions (empty = none), prepended
	// as a system message on every turn — the other half of a persona (a "Coder"
	// brief vs a "Creative" brief). In v2 a Taita persona prompt composes above it.
	SystemPrompt string `json:"systemPrompt,omitempty"`
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

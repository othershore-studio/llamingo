// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ModelConfig is one enabled image model: its checkpoint name, its DiT modules,
// and optional recipe overrides. Anything left zero falls back to the name-based
// default recipe (see DefaultRecipe), so the minimal entry is just a name (+ the
// modules a modern DiT needs).
type ModelConfig struct {
	Name      string   `json:"name"`
	Modules   []string `json:"modules,omitempty"`
	Steps     int      `json:"steps,omitempty"`
	CFG       float64  `json:"cfg,omitempty"`
	Sampler   string   `json:"sampler,omitempty"`
	Scheduler string   `json:"scheduler,omitempty"`
	Width     int      `json:"width,omitempty"`
	Height    int      `json:"height,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// Recipe is this model's effective recipe: the name-based default with any
// configured overrides applied.
func (m ModelConfig) Recipe() Recipe {
	r := DefaultRecipe(m.Name)
	if m.Steps > 0 {
		r.Steps = m.Steps
	}
	if m.CFG > 0 {
		r.CFG = m.CFG
	}
	if m.Sampler != "" {
		r.Sampler = m.Sampler
	}
	if m.Scheduler != "" {
		r.Scheduler = m.Scheduler
	}
	if m.Width > 0 {
		r.Width = m.Width
	}
	if m.Height > 0 {
		r.Height = m.Height
	}
	if m.Note != "" {
		r.Note = m.Note
	}
	return r
}

// Config is the image-generation configuration — the enabled models and which is
// the default. It is loaded from <root>/imagegen.json (hand-edited for now; a
// connect-and-configure UI comes later).
type Config struct {
	Default string        `json:"default"`
	Models  []ModelConfig `json:"models"`
}

// LoadConfig reads the image-gen config from path. A missing file is an empty
// Config (image generation just has no models configured yet), not an error.
func LoadConfig(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("imagegen: read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("imagegen: parse config: %w", err)
	}
	return c, nil
}

// Model returns the named model's config, or the default when name is empty (or
// the lone model when there is exactly one and no default). ok is false when the
// name is unknown or there is nothing usable to choose.
func (c Config) Model(name string) (ModelConfig, bool) {
	if name == "" {
		name = c.Default
	}
	if name == "" && len(c.Models) == 1 {
		return c.Models[0], true
	}
	for _, m := range c.Models {
		if strings.EqualFold(m.Name, name) {
			return m, true
		}
	}
	return ModelConfig{}, false
}

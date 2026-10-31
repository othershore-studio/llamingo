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
	AutoPick  *bool    `json:"autoPick,omitempty"` // may it be auto-selected? nil = infer from the name
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

// AutoPickable reports whether the model may be selected automatically — by the
// model from list_image_models, or as the sticky default when a generation names
// none. A configured AutoPick wins; otherwise it is inferred from the name
// (modern DiTs are heavy on a shared GPU, so off; SDXL/SD1.5 are light, so on).
// See defaultAutoPick.
func (m ModelConfig) AutoPickable() bool {
	if m.AutoPick != nil {
		return *m.AutoPick
	}
	return defaultAutoPick(m.Name)
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

// SaveConfig writes the image-gen config to path as indented JSON, creating or
// replacing the file. It is the counterpart to LoadConfig — the connect-and-
// configure UI and the Forge prepopulate persist through it.
func SaveConfig(path string, c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("imagegen: marshal config: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("imagegen: write config: %w", err)
	}
	return nil
}

// Merge reconciles the configured models with the checkpoints Forge currently
// reports (see ForgeNeo.ListModels). It is both the first-time seed (from an empty
// Config) and the refresh:
//
//   - a checkpoint already configured is kept verbatim — the user's AutoPick, the
//     chosen default, and any recipe overrides survive a refresh;
//   - a checkpoint new to Forge is added with name-inferred defaults (heavy DiTs
//     stay opt-in — see ModelConfig.AutoPickable);
//   - a configured model Forge no longer has is dropped (it can't be generated).
//
// Order follows Forge's list, so the UI stays stable. The default is kept safe
// (see SafeDefault), so a fresh seed never auto-picks a heavy model.
func (c Config) Merge(forgeNames []string) Config {
	existing := make(map[string]ModelConfig, len(c.Models))
	for _, m := range c.Models {
		existing[strings.ToLower(m.Name)] = m
	}
	out := Config{Default: c.Default}
	for _, name := range forgeNames {
		if m, ok := existing[strings.ToLower(name)]; ok {
			out.Models = append(out.Models, m)
		} else {
			out.Models = append(out.Models, ModelConfig{Name: name})
		}
	}
	out.Default = SafeDefault(out, out.Default)
	return out
}

// SafeDefault returns prefer when it names a configured, auto-pickable model; else
// the first auto-pickable model (a light SDXL/SD1.5 that won't OOM the shared
// GPU); else "". It keeps an unnamed generation's sticky default from ever landing
// on a heavy DiT the user has not opted into.
func SafeDefault(c Config, prefer string) string {
	if prefer != "" {
		if m, ok := c.Model(prefer); ok && m.AutoPickable() {
			return prefer
		}
	}
	for _, m := range c.Models {
		if m.AutoPickable() {
			return m.Name
		}
	}
	return ""
}

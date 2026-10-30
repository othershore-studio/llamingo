// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissing(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing config should be empty, not an error: %v", err)
	}
	if c.Default != "" || len(c.Models) != 0 {
		t.Errorf("missing config = %+v, want empty", c)
	}
}

func TestLoadConfigAndModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imagegen.json")
	if err := os.WriteFile(path, []byte(`{
		"default": "z_image_turbo-Q8_0",
		"models": [
			{"name": "z_image_turbo-Q8_0", "modules": ["qwen3_4b", "z_image_ae"], "steps": 12},
			{"name": "DreamShaperXL_Turbo_SFW"}
		]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 2 || c.Default != "z_image_turbo-Q8_0" {
		t.Fatalf("loaded config = %+v", c)
	}

	// The default resolves; a configured override wins over the name default.
	m, ok := c.Model("")
	if !ok || m.Name != "z_image_turbo-Q8_0" {
		t.Fatalf("Model(\"\") = %+v, %v; want the default", m, ok)
	}
	if r := m.Recipe(); r.Steps != 12 || r.CFG != 1 || r.Sampler != "Euler" || len(m.Modules) != 2 {
		t.Errorf("recipe = %+v (modules %v); want steps 12 (override), CFG 1 + Euler (default)", r, m.Modules)
	}

	// A named model resolves case-insensitively; an unknown one does not.
	if _, ok := c.Model("dreamshaperxl_turbo_sfw"); !ok {
		t.Error("named model should resolve case-insensitively")
	}
	if _, ok := c.Model("nonexistent"); ok {
		t.Error("unknown model should not resolve")
	}
}

func TestModelLoneDefault(t *testing.T) {
	c := Config{Models: []ModelConfig{{Name: "only_model"}}}
	if m, ok := c.Model(""); !ok || m.Name != "only_model" {
		t.Errorf("a lone model should be used without an explicit default; got %+v, %v", m, ok)
	}
	if _, ok := (Config{}).Model(""); ok {
		t.Error("no models → no resolution")
	}
}

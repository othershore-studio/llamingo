// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen_test

import (
	"path/filepath"
	"testing"

	"github.com/othershore-studio/llamingo/internal/imagegen"
)

// Merge on an empty config is a fresh seed: every Forge checkpoint becomes a
// model, auto-pick is inferred from the name (light SDXL on, heavy DiT off), and
// the default is the first auto-pickable one — safe by default.
func TestMergeSeedsEmptyConfig(t *testing.T) {
	c := imagegen.Config{}.Merge([]string{"z_image_turbo", "Juggernaut-XI", "DreamShaper"})
	if len(c.Models) != 3 {
		t.Fatalf("models = %d, want 3", len(c.Models))
	}
	if c.Models[0].AutoPickable() {
		t.Errorf("z_image is a heavy DiT; it should not be auto-pickable")
	}
	if !c.Models[1].AutoPickable() {
		t.Errorf("Juggernaut (SDXL) should be auto-pickable")
	}
	if c.Default != "Juggernaut-XI" {
		t.Errorf("default = %q, want the first auto-pickable model (Juggernaut-XI)", c.Default)
	}
}

// Merge preserves the user's choices across a refresh: an explicit AutoPick, a
// recipe override, and the chosen default survive; checkpoints new to Forge are
// added; ones Forge no longer has are dropped.
func TestMergePreservesUserChoices(t *testing.T) {
	yes := true
	prev := imagegen.Config{
		Default: "z_image_turbo",
		Models: []imagegen.ModelConfig{
			{Name: "z_image_turbo", AutoPick: &yes, Steps: 12}, // opted in + a recipe override
			{Name: "OldModel"},                                 // no longer in Forge
		},
	}
	got := prev.Merge([]string{"z_image_turbo", "Juggernaut-XI"})
	if len(got.Models) != 2 {
		t.Fatalf("models = %d, want 2 (OldModel dropped, Juggernaut added)", len(got.Models))
	}
	z, ok := got.Model("z_image_turbo")
	if !ok || z.AutoPick == nil || !*z.AutoPick || z.Steps != 12 {
		t.Errorf("z_image lost its opt-in or recipe override: %+v", z)
	}
	if got.Default != "z_image_turbo" {
		t.Errorf("default = %q, want it preserved (the user opted z_image in)", got.Default)
	}
	if _, ok := got.Model("OldModel"); ok {
		t.Errorf("OldModel should be dropped — Forge no longer has it")
	}
	if _, ok := got.Model("Juggernaut-XI"); !ok {
		t.Errorf("Juggernaut-XI should be added")
	}
}

// SafeDefault refuses a default that is not auto-pickable (a heavy DiT the user
// has not opted into), falling back to the first auto-pickable model.
func TestSafeDefaultRefusesHeavyDefault(t *testing.T) {
	c := imagegen.Config{Models: []imagegen.ModelConfig{
		{Name: "flux-klein"},    // heavy, inferred off
		{Name: "Juggernaut-XI"}, // light, inferred on
	}}
	if d := imagegen.SafeDefault(c, "flux-klein"); d != "Juggernaut-XI" {
		t.Errorf("default = %q, want the fallback to the auto-pickable Juggernaut-XI", d)
	}
}

// SaveConfig and LoadConfig round-trip, including an explicit AutoPick.
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imagegen.json")
	yes := true
	want := imagegen.Config{Default: "A", Models: []imagegen.ModelConfig{{Name: "A", AutoPick: &yes}, {Name: "B"}}}
	if err := imagegen.SaveConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := imagegen.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Default != "A" || len(got.Models) != 2 || got.Models[0].AutoPick == nil || !*got.Models[0].AutoPick {
		t.Errorf("round-trip = %+v", got)
	}
}

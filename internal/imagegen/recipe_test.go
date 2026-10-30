// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen

import "testing"

func TestDefaultRecipe(t *testing.T) {
	cases := []struct {
		name  string
		steps int
		cfg   float64
		samp  string
		sched string
		w     int
		style string
	}{
		{"z_image_turbo-Q8_0", 8, 1, "Euler", "simple", 1024, "natural"},
		{"flux-2-klein-4b", 8, 1, "Euler", "simple", 1024, "natural"},
		{"Juggernaut-XI", 28, 5, "DPM++ 2M", "karras", 1024, "phrase"},
		{"NoobAI-XL-v1.1", 26, 6, "Euler a", "normal", 1024, "booru"},
		{"DreamShaper_8_pruned", 25, 7, "DPM++ 2M", "karras", 512, "phrase"}, // SD1.5 → 512²
		{"RealVisXL_V5.0_Lightning", 8, 2, "DPM++ SDE", "karras", 1024, "phrase"}, // lightning variant
		{"DreamShaperXL_Turbo_SFW", 8, 2, "DPM++ SDE", "karras", 1024, "phrase"},   // turbo variant
		{"some_unknown_model", 28, 6, "DPM++ 2M", "karras", 1024, "phrase"},        // generic default
	}
	for _, c := range cases {
		r := DefaultRecipe(c.name)
		if r.Steps != c.steps || r.CFG != c.cfg || r.Sampler != c.samp || r.Scheduler != c.sched || r.Width != c.w || r.PromptStyle != c.style {
			t.Errorf("DefaultRecipe(%q) = %+v, want steps=%d cfg=%g sampler=%q scheduler=%q w=%d style=%q",
				c.name, r, c.steps, c.cfg, c.samp, c.sched, c.w, c.style)
		}
	}
}

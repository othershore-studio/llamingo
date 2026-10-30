// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen

import "strings"

// Recipe is a model's sampling recipe — everything but the prompt and the
// checkpoint. The bundled defaults below are a guide keyed off the model name so
// the user doesn't start from nothing; they can override any field in the config.
// Never a guarantee — the recipes come from the yuki-ai-stack runbook's table.
type Recipe struct {
	Steps       int
	CFG         float64
	Sampler     string // Forge's exact name, e.g. "Euler", "DPM++ 2M"
	Scheduler   string // e.g. "simple", "karras"
	Width       int
	Height      int
	PromptStyle string // "natural" | "booru" | "phrase"
	Note        string
}

// DefaultRecipe guesses a sensible recipe from a model's name. Modern DiTs (LLM
// encoders) are natural-language, CFG 1, few-step; SDXL/SD1.5 are phrase/booru
// with higher CFG and steps; a turbo/lightning/flash in the name drops the step
// count. All 1024² except SD1.5.
func DefaultRecipe(name string) Recipe {
	n := strings.ToLower(name)
	switch {
	case has(n, "z_image", "z-image", "zimage"):
		return Recipe{8, 1, "Euler", "simple", 1024, 1024, "natural", "CFG 1 → negative prompt ignored"}
	case has(n, "flux", "klein"):
		return Recipe{8, 1, "Euler", "simple", 1024, 1024, "natural", "distilled; bare prompt skews anime — specify for photoreal"}
	case has(n, "qwen"):
		return Recipe{10, 1, "Euler", "simple", 1024, 1024, "natural", "prompt-hungry — be very specific; CFG 1 → negative ignored"}
	case has(n, "anima") && has(n, "aesthetic"):
		return Recipe{40, 4.5, "ER SDE", "beta", 1024, 1024, "natural", "anime quality variant (slow refine)"}
	case has(n, "anima"):
		return Recipe{10, 1, "Euler", "simple", 1024, 1024, "natural", "anime-only; CFG 1 → negative ignored"}
	case has(n, "noob", "illustrious", "nova", "perfectdeliberate"):
		return variant(n, Recipe{26, 6, "Euler a", "normal", 1024, 1024, "booru", "danbooru tags"})
	case has(n, "juggernaut", "realvis"):
		return variant(n, Recipe{28, 5, "DPM++ 2M", "karras", 1024, 1024, "phrase", "SDXL photoreal"})
	case has(n, "dreamshaper") && !has(n, "xl"):
		return variant(n, Recipe{25, 7, "DPM++ 2M", "karras", 512, 512, "phrase", "SD1.5 (512²)"})
	default:
		return variant(n, Recipe{28, 6, "DPM++ 2M", "karras", 1024, 1024, "phrase", "generic SDXL default — verify"})
	}
}

// variant lowers steps/CFG and switches the sampler for a turbo/lightning/flash
// few-step checkpoint (not applied to the modern DiTs, which are already few-step).
func variant(n string, r Recipe) Recipe {
	if has(n, "turbo", "lightning", "lightening", "flash", "hyper", "lcm") {
		r.Steps, r.CFG, r.Sampler, r.Scheduler = 8, 2, "DPM++ SDE", "karras"
		r.Note = "few-step variant — " + r.Note
	}
	return r
}

func has(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

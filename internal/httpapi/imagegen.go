// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/othershore-studio/llamingo/internal/imagegen"
)

// imagegenConfigPath is where the image-model config lives — next to the app
// config under the store root. Shared with the tool wiring in permission.go.
func (s *Server) imagegenConfigPath() string {
	return filepath.Join(s.st.Root(), "imagegen.json")
}

// forgeBaseURL resolves the Forge Neo base URL: the one just typed in the request
// (so the Settings UI can fetch before Save), else the saved setting, else the
// LLAMINGO_FORGE_URL env fallback.
func (s *Server) forgeBaseURL(typed string) string {
	if u := strings.TrimSpace(typed); u != "" {
		return u
	}
	cfg, _ := s.st.Config()
	if cfg.ForgeURL != "" {
		return cfg.ForgeURL
	}
	return os.Getenv("LLAMINGO_FORGE_URL")
}

// imagegenModelView is one model as the Settings UI needs it: its name, whether it
// may be auto-selected (the effective AutoPickable), whether it is the default, its
// prompt style / note for display, and its effective recipe (the name-inferred
// defaults with any overrides applied) so the editor can pre-fill the fields.
type imagegenModelView struct {
	Name        string   `json:"name"`
	AutoPick    bool     `json:"autoPick"`
	Default     bool     `json:"default"`
	PromptStyle string   `json:"promptStyle"`
	Note        string   `json:"note"`
	Steps       int      `json:"steps"`
	CFG         float64  `json:"cfg"`
	Sampler     string   `json:"sampler"`
	Scheduler   string   `json:"scheduler"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
	Modules     []string `json:"modules"`
}

func imagegenView(cfg imagegen.Config) map[string]any {
	models := make([]imagegenModelView, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		r := m.Recipe()
		mods := m.Modules
		if mods == nil {
			mods = []string{}
		}
		models = append(models, imagegenModelView{
			Name:        m.Name,
			AutoPick:    m.AutoPickable(),
			Default:     strings.EqualFold(m.Name, cfg.Default),
			PromptStyle: r.PromptStyle,
			Note:        r.Note,
			Steps:       r.Steps,
			CFG:         r.CFG,
			Sampler:     r.Sampler,
			Scheduler:   r.Scheduler,
			Width:       r.Width,
			Height:      r.Height,
			Modules:     mods,
		})
	}
	return map[string]any{"default": cfg.Default, "models": models}
}

// GET /api/imagegen — the configured image models (name, whether auto-pickable,
// which is the default) for the Settings UI.
func (s *Server) handleGetImagegen(w http.ResponseWriter, r *http.Request) {
	cfg, err := imagegen.LoadConfig(s.imagegenConfigPath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, imagegenView(cfg))
}

// POST /api/imagegen/fetch {forgeUrl?} — fetch Forge's checkpoints, merge them
// into the saved config (keeping the user's choices — see imagegen.Config.Merge),
// persist, and return the merged list. This is both the first-connect seed and
// the "Refresh from Forge" action.
func (s *Server) handleFetchImagegen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ForgeURL string `json:"forgeUrl"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // body is optional
	url := s.forgeBaseURL(body.ForgeURL)
	if url == "" {
		http.Error(w, "no Forge URL configured", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	names, err := imagegen.NewForgeNeo(url).ListModels(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	cfg, err := imagegen.LoadConfig(s.imagegenConfigPath())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	merged := cfg.Merge(names)
	if err := imagegen.SaveConfig(s.imagegenConfigPath(), merged); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, imagegenView(merged))
}

// PUT /api/imagegen {default, models:[{name, autoPick, steps, cfg, sampler,
// scheduler, width, height, modules}]} — persist the user's image-model choices
// from the Settings UI: which models may auto-select, the default, and each
// model's recipe. Recipe fields are stored as overrides only where they differ
// from the name-inferred default (see ModelConfig.WithRecipe); the default is kept
// safe (see imagegen.SafeDefault).
func (s *Server) handlePutImagegen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Default string `json:"default"`
		Models  []struct {
			Name      string   `json:"name"`
			AutoPick  bool     `json:"autoPick"`
			Steps     int      `json:"steps"`
			CFG       float64  `json:"cfg"`
			Sampler   string   `json:"sampler"`
			Scheduler string   `json:"scheduler"`
			Width     int      `json:"width"`
			Height    int      `json:"height"`
			Modules   []string `json:"modules"`
		} `json:"models"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	prev, _ := imagegen.LoadConfig(s.imagegenConfigPath())
	prevByName := make(map[string]imagegen.ModelConfig, len(prev.Models))
	for _, m := range prev.Models {
		prevByName[strings.ToLower(m.Name)] = m
	}
	var cfg imagegen.Config
	for _, bm := range body.Models {
		m := prevByName[strings.ToLower(bm.Name)] // preserve the note; zero value if new
		m.Name = bm.Name
		ap := bm.AutoPick
		m.AutoPick = &ap
		m = m.WithRecipe(imagegen.Recipe{
			Steps: bm.Steps, CFG: bm.CFG, Sampler: bm.Sampler, Scheduler: bm.Scheduler,
			Width: bm.Width, Height: bm.Height,
		}, bm.Modules)
		cfg.Models = append(cfg.Models, m)
	}
	cfg.Default = imagegen.SafeDefault(cfg, body.Default)
	if err := imagegen.SaveConfig(s.imagegenConfigPath(), cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, imagegenView(cfg))
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ForgeNeo generates images through a Forge Neo server's /sdapi/v1/txt2img
// endpoint (the maintained forge-classic "neo" fork). It implements Generator.
// The per-model recipe — checkpoint, steps, CFG, sampler, scheduler, DiT modules,
// resolution — arrives in Options and is passed through verbatim; nothing is
// guessed here. The Generator seam stays open for ComfyUI or other backends.
type ForgeNeo struct {
	baseURL string
	client  *http.Client
}

// NewForgeNeo returns a Forge Neo generator for the given base URL (e.g.
// https://forge.example.com). Image generation can take tens of seconds, so the
// client timeout is generous.
func NewForgeNeo(baseURL string) *ForgeNeo {
	return &ForgeNeo{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
}

type forgeTxt2ImgRequest struct {
	Prompt                            string         `json:"prompt"`
	NegativePrompt                    string         `json:"negative_prompt,omitempty"`
	Steps                             int            `json:"steps,omitempty"`
	CfgScale                          float64        `json:"cfg_scale,omitempty"`
	SamplerName                       string         `json:"sampler_name,omitempty"`
	Scheduler                         string         `json:"scheduler,omitempty"`
	Width                             int            `json:"width,omitempty"`
	Height                            int            `json:"height,omitempty"`
	Seed                              int            `json:"seed"`
	OverrideSettings                  map[string]any `json:"override_settings,omitempty"`
	OverrideSettingsRestoreAfterwards bool           `json:"override_settings_restore_afterwards"`
}

type forgeTxt2ImgResponse struct {
	Images []string `json:"images"`
	Info   string   `json:"info"` // a JSON string carrying the seed, etc.
}

// Generate runs one txt2img and returns the image plus the seed Forge chose.
func (f *ForgeNeo) Generate(ctx context.Context, prompt string, opts Options) (Image, error) {
	if f.baseURL == "" {
		return Image{}, fmt.Errorf("imagegen: no Forge URL configured")
	}
	seed := opts.Seed
	if seed <= 0 {
		seed = -1 // Forge: pick a random seed
	}
	reqBody := forgeTxt2ImgRequest{
		Prompt:                            prompt,
		NegativePrompt:                    opts.Negative,
		Steps:                             opts.Steps,
		CfgScale:                          opts.CFG,
		SamplerName:                       opts.Sampler,
		Scheduler:                         opts.Scheduler,
		Width:                             opts.Width,
		Height:                            opts.Height,
		Seed:                              seed,
		OverrideSettingsRestoreAfterwards: true,
	}
	override := map[string]any{}
	if opts.Model != "" {
		override["sd_model_checkpoint"] = opts.Model
	}
	if len(opts.Modules) > 0 {
		override["forge_additional_modules"] = opts.Modules
	}
	if len(override) > 0 {
		reqBody.OverrideSettings = override
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return Image{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/sdapi/v1/txt2img", bytes.NewReader(body))
	if err != nil {
		return Image{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return Image{}, fmt.Errorf("imagegen: forge request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Image{}, fmt.Errorf("imagegen: forge http %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var out forgeTxt2ImgResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Image{}, fmt.Errorf("imagegen: decode forge response: %w", err)
	}
	if len(out.Images) == 0 {
		return Image{}, fmt.Errorf("imagegen: forge returned no image")
	}
	data, err := base64.StdEncoding.DecodeString(stripDataURL(out.Images[0]))
	if err != nil {
		return Image{}, fmt.Errorf("imagegen: decode image: %w", err)
	}
	img := Image{Data: data, MIME: "image/png"}
	// info is a JSON string; its seed is the one to reuse.
	var info struct {
		Seed int64 `json:"seed"`
	}
	if json.Unmarshal([]byte(out.Info), &info) == nil {
		img.Seed = info.Seed
	}
	return img, nil
}

// stripDataURL drops a leading "data:image/...;base64," prefix if present, so the
// remainder decodes as raw base64.
func stripDataURL(s string) string {
	if strings.HasPrefix(s, "data:") {
		if i := strings.IndexByte(s, ','); i >= 0 {
			return s[i+1:]
		}
	}
	return s
}

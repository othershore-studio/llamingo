// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package imagegen_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/othershore-studio/llamingo/internal/imagegen"
)

// The Forge generator sends the recipe verbatim to /sdapi/v1/txt2img and returns
// the decoded image plus the seed Forge reports.
func TestForgeNeoGenerate(t *testing.T) {
	const pngBytes = "\x89PNG\r\n\x1a\nFAKE"
	var gotPath string
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&gotReq)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"images": []string{base64.StdEncoding.EncodeToString([]byte(pngBytes))},
			"info":   `{"seed": 777, "all_seeds": [777]}`,
		})
	}))
	defer srv.Close()

	g := imagegen.NewForgeNeo(srv.URL)
	img, err := g.Generate(context.Background(), "a red fox in snow", imagegen.Options{
		Model: "z_image_turbo", Steps: 8, CFG: 1, Sampler: "Euler", Scheduler: "simple",
		Modules: []string{"qwen3_4b", "z_image_ae"}, Width: 1024, Height: 1024,
		Negative: "blurry", Seed: 777,
	})
	if err != nil {
		t.Fatal(err)
	}

	if string(img.Data) != pngBytes {
		t.Errorf("image data = %q, want the decoded PNG", img.Data)
	}
	if img.Seed != 777 {
		t.Errorf("seed = %d, want 777", img.Seed)
	}
	if gotPath != "/sdapi/v1/txt2img" {
		t.Errorf("posted to %q, want /sdapi/v1/txt2img", gotPath)
	}

	// The recipe rode in the request verbatim (JSON numbers decode as float64).
	if gotReq["steps"] != float64(8) || gotReq["cfg_scale"] != float64(1) ||
		gotReq["sampler_name"] != "Euler" || gotReq["scheduler"] != "simple" ||
		gotReq["prompt"] != "a red fox in snow" || gotReq["negative_prompt"] != "blurry" {
		t.Errorf("request recipe = %+v", gotReq)
	}
	ov, _ := gotReq["override_settings"].(map[string]any)
	if ov == nil || ov["sd_model_checkpoint"] != "z_image_turbo" {
		t.Errorf("override_settings = %+v, want the checkpoint", gotReq["override_settings"])
	}
	mods, _ := ov["forge_additional_modules"].([]any)
	if len(mods) != 2 || mods[0] != "qwen3_4b" {
		t.Errorf("forge_additional_modules = %+v, want the 2 DiT modules", ov["forge_additional_modules"])
	}
}

// ListModels fetches Forge's checkpoints from /sdapi/v1/sd-models and returns each
// model_name (the value sd_model_checkpoint accepts), falling back to title.
func TestForgeNeoListModels(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"title": "Juggernaut-XI.safetensors [abc123]", "model_name": "Juggernaut-XI-byRunDiffusion"},
			{"title": "z_image_turbo.safetensors", "model_name": "z_image_turbo"},
		})
	}))
	defer srv.Close()

	names, err := imagegen.NewForgeNeo(srv.URL).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/sdapi/v1/sd-models" {
		t.Errorf("GET %q, want /sdapi/v1/sd-models", gotPath)
	}
	if len(names) != 2 || names[0] != "Juggernaut-XI-byRunDiffusion" || names[1] != "z_image_turbo" {
		t.Errorf("names = %v, want the two model_names", names)
	}
}

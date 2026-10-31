// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

type imagegenResp struct {
	Default string `json:"default"`
	Models  []struct {
		Name     string `json:"name"`
		AutoPick bool   `json:"autoPick"`
		Default  bool   `json:"default"`
	} `json:"models"`
}

// POST /api/imagegen/fetch seeds the image-model config from Forge: every
// checkpoint becomes a model, heavy DiTs stay opt-in, a safe default is chosen,
// and it persists so GET returns the same.
func TestImagegenFetchSeedsConfig(t *testing.T) {
	forge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sdapi/v1/sd-models" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"model_name": "z_image_turbo"},
			{"model_name": "Juggernaut-XI"},
		})
	}))
	t.Cleanup(forge.Close)

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	// Fetch with the just-typed Forge URL (as the Settings UI does before Save).
	resp, err := http.Post(api.URL+"/api/imagegen/fetch", "application/json",
		strings.NewReader(`{"forgeUrl":"`+forge.URL+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fetch status = %d", resp.StatusCode)
	}
	var got imagegenResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 {
		t.Fatalf("models = %d, want 2", len(got.Models))
	}
	auto := map[string]bool{}
	for _, m := range got.Models {
		auto[m.Name] = m.AutoPick
	}
	if auto["z_image_turbo"] {
		t.Errorf("z_image_turbo (heavy DiT) should not be auto-pickable")
	}
	if !auto["Juggernaut-XI"] {
		t.Errorf("Juggernaut-XI (SDXL) should be auto-pickable")
	}
	if got.Default != "Juggernaut-XI" {
		t.Errorf("default = %q, want the safe Juggernaut-XI", got.Default)
	}

	// It persisted: GET returns the same two models.
	resp2, err := http.Get(api.URL + "/api/imagegen")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	var got2 imagegenResp
	if err := json.NewDecoder(resp2.Body).Decode(&got2); err != nil {
		t.Fatal(err)
	}
	if len(got2.Models) != 2 {
		t.Errorf("GET after fetch: models = %d, want 2 (persisted)", len(got2.Models))
	}
}

// PUT /api/imagegen persists the user's choices: opting a heavy model in makes it
// auto-pickable and lets it be the default.
func TestImagegenPutSavesChoices(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/imagegen",
		strings.NewReader(`{"default":"flux-klein","models":[{"name":"flux-klein","autoPick":true},{"name":"Juggernaut-XI","autoPick":true}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("put status = %d", resp.StatusCode)
	}
	var got imagegenResp
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	// flux is normally opt-out, but the user enabled it — so it is auto-pickable
	// and may be the default.
	if got.Default != "flux-klein" {
		t.Errorf("default = %q, want flux-klein (the user opted it in)", got.Default)
	}
	for _, m := range got.Models {
		if m.Name == "flux-klein" && !m.AutoPick {
			t.Errorf("flux-klein should be auto-pickable after the user enabled it")
		}
	}
}

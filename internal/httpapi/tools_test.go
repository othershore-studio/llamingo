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

type toolsResp struct {
	Tools []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	} `json:"tools"`
}

func toolEnabled(tr toolsResp, name string) (found, enabled bool) {
	for _, t := range tr.Tools {
		if t.Name == name {
			return true, t.Enabled
		}
	}
	return false, false
}

// The curation endpoints list the advertised tools and round-trip a disabled set:
// after disabling a tool it reports enabled=false, and the change persists (the
// registry reads the set fresh each turn).
func TestToolsCurationRoundTrip(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	var got toolsResp
	getJSONInto(t, api.URL+"/api/tools", &got)
	if len(got.Tools) == 0 {
		t.Fatal("expected some tools listed (at least the native write_memory)")
	}
	if found, enabled := toolEnabled(got, "write_memory"); !found || !enabled {
		t.Fatalf("write_memory found=%v enabled=%v, want it listed and enabled", found, enabled)
	}

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/tools", strings.NewReader(`{"disabled":["write_memory"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", resp.StatusCode)
	}

	getJSONInto(t, api.URL+"/api/tools", &got)
	if _, enabled := toolEnabled(got, "write_memory"); enabled {
		t.Error("write_memory should be disabled after the PUT")
	}
}

func getJSONInto(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d", url, resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

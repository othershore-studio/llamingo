// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !windows

package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// On a platform with no autostart implementation, the endpoint reports the feature
// unsupported and a request to enable it fails loudly rather than silently pretending.
// Gated to non-Windows (CI is Linux) so the test never writes to a real Windows
// registry Run key; the Windows path is exercised live.
func TestAutostartUnsupported(t *testing.T) {
	api := newAPI(t)

	resp, err := http.Get(api.URL + "/api/autostart")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/autostart status = %d", resp.StatusCode)
	}
	var st struct {
		Supported bool `json:"supported"`
		Enabled   bool `json:"enabled"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		t.Fatal(err)
	}
	if st.Supported || st.Enabled {
		t.Errorf("autostart status = %+v, want unsupported and disabled", st)
	}

	req, err := http.NewRequest(http.MethodPut, api.URL+"/api/autostart", strings.NewReader(`{"enabled":true,"minimized":false}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("content-type", "application/json")
	put, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer put.Body.Close()
	if put.StatusCode != http.StatusInternalServerError {
		t.Fatalf("PUT enable on an unsupported platform status = %d, want 500", put.StatusCode)
	}
}

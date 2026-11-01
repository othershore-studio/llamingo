// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
)

type connectionsResp struct {
	ActiveURL         string   `json:"activeUrl"`
	ActiveTemperature *float64 `json:"activeTemperature"`
	Connections       []struct {
		Name        string   `json:"name"`
		URL         string   `json:"url"`
		Token       string   `json:"token"`
		Temperature *float64 `json:"temperature"`
	} `json:"connections"`
}

// The connections list round-trips, always includes the active connection, and
// /activate hot-swaps which one is live.
func TestConnections(t *testing.T) {
	api := newAPI(t)

	// Fresh: the live (fake) connection is surfaced even though nothing is saved.
	var g connectionsResp
	getJSONInto(t, api.URL+"/api/connections", &g)
	if g.ActiveURL == "" {
		t.Fatal("activeUrl is empty, want the backend's url")
	}
	if len(g.Connections) != 1 || g.Connections[0].URL != g.ActiveURL {
		t.Fatalf("connections = %+v, want the active one synthesized in", g.Connections)
	}

	// Save a named list.
	put := `{"connections":[{"name":"Lab","url":"http://lab.invalid:8080","token":"abc","temperature":0.2},{"name":"Local","url":"http://localhost:8080"}]}`
	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/connections", strings.NewReader(put))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT connections = %d, want 204", resp.StatusCode)
	}

	// The active (fake) url isn't among the saved two, so it's still surfaced → 3.
	getJSONInto(t, api.URL+"/api/connections", &g)
	if len(g.Connections) != 3 {
		t.Fatalf("after save, connections = %+v, want 3 (2 saved + active)", g.Connections)
	}
	// Lab's per-connection sampling round-trips (the persona knob).
	var labTemp *float64
	for _, c := range g.Connections {
		if c.Name == "Lab" {
			labTemp = c.Temperature
		}
	}
	if labTemp == nil || *labTemp != 0.2 {
		t.Errorf("Lab temperature did not round-trip: %v", labTemp)
	}

	// Activate the Lab connection → it becomes live and is persisted as active.
	req2, _ := http.NewRequest(http.MethodPost, api.URL+"/api/connections/activate", strings.NewReader(`{"url":"http://lab.invalid:8080","token":"abc","temperature":0.2}`))
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNoContent {
		t.Fatalf("activate = %d, want 204", resp2.StatusCode)
	}

	getJSONInto(t, api.URL+"/api/connections", &g)
	if g.ActiveURL != "http://lab.invalid:8080" {
		t.Errorf("after activate, activeUrl = %q, want the Lab url", g.ActiveURL)
	}
	if g.ActiveTemperature == nil || *g.ActiveTemperature != 0.2 {
		t.Errorf("after activate, activeTemperature = %v, want 0.2", g.ActiveTemperature)
	}
	// Lab is now both active and saved, so no synthesized row → the saved 2.
	if len(g.Connections) != 2 {
		t.Errorf("after activate, connections = %+v, want the 2 saved", g.Connections)
	}
}

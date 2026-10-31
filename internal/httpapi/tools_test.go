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
	Groups []struct {
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		Tools []struct {
			Name    string `json:"name"`
			Enabled bool   `json:"enabled"`
		} `json:"tools"`
	} `json:"groups"`
}

// find reports whether a tool is listed anywhere and whether it is enabled.
func (tr toolsResp) find(name string) (found, enabled bool) {
	for _, g := range tr.Groups {
		for _, t := range g.Tools {
			if t.Name == name {
				return true, t.Enabled
			}
		}
	}
	return false, false
}

// Per-chat tool curation: the tools list is grouped (a System group with the
// built-in tools), disabling a tool for a chat round-trips through its meta.json,
// and it does not affect other chats.
func TestConversationToolsCuration(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	id := createChat(t, api, "Tools")

	var got toolsResp
	getJSONInto(t, api.URL+"/api/conversations/"+id+"/tools", &got)
	if len(got.Groups) == 0 {
		t.Fatal("expected at least the System group")
	}
	if got.Groups[0].Kind != "system" {
		t.Errorf("first group kind = %q, want system", got.Groups[0].Kind)
	}
	if found, enabled := got.find("write_memory"); !found || !enabled {
		t.Fatalf("write_memory found=%v enabled=%v, want listed and enabled", found, enabled)
	}

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/conversations/"+id+"/tools", strings.NewReader(`{"disabled":["write_memory"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", resp.StatusCode)
	}

	getJSONInto(t, api.URL+"/api/conversations/"+id+"/tools", &got)
	if _, enabled := got.find("write_memory"); enabled {
		t.Error("write_memory should be disabled for this chat after the PUT")
	}

	// A different chat is unaffected — curation is per-chat.
	id2 := createChat(t, api, "Other")
	getJSONInto(t, api.URL+"/api/conversations/"+id2+"/tools", &got)
	if _, enabled := got.find("write_memory"); !enabled {
		t.Error("write_memory should still be enabled in a different chat")
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

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

type projectResp struct {
	InProject   bool `json:"inProject"`
	SystemTools []struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	} `json:"systemTools"`
}

func (pr projectResp) enabled(name string) bool {
	for _, t := range pr.SystemTools {
		if t.Name == name {
			return t.Enabled
		}
	}
	return false
}

// A project's system-tool defaults round-trip through .llamingo/project.json; a
// free chat reports inProject=false; and a new session in the project inherits the
// defaults.
func TestProjectToolDefaults(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj, err := st.OpenProject(t.TempDir(), "Proj")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.NewSession(proj.ID, "Sess")
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	// A free chat is not in a project.
	free := createChat(t, api, "Free")
	var pr projectResp
	getJSONInto(t, api.URL+"/api/project?conv="+free, &pr)
	if pr.InProject {
		t.Error("a free chat should report inProject=false")
	}

	// The project session is in a project; write_memory starts enabled.
	getJSONInto(t, api.URL+"/api/project?conv="+sess.ID, &pr)
	if !pr.InProject {
		t.Fatal("the project session should report inProject=true")
	}
	if !pr.enabled("write_memory") {
		t.Error("write_memory should start enabled for the project")
	}

	// Set the project default: write_memory off.
	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/project?conv="+sess.ID, strings.NewReader(`{"disabledSystem":["write_memory"]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT status = %d, want 204", resp.StatusCode)
	}

	getJSONInto(t, api.URL+"/api/project?conv="+sess.ID, &pr)
	if pr.enabled("write_memory") {
		t.Error("write_memory should be off after setting the project default")
	}

	// A NEW session inherits the default (write_memory in its DisabledTools).
	sess2, err := st.NewSession(proj.ID, "Sess2")
	if err != nil {
		t.Fatal(err)
	}
	dt, _ := st.ConversationTools(sess2.ID)
	if !slices.Contains(dt, "write_memory") {
		t.Errorf("new session DisabledTools = %v, want it to inherit write_memory off", dt)
	}
}

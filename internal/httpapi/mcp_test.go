// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/store"
)

type mcpResp struct {
	Servers []struct {
		Name     string `json:"name"`
		URL      string `json:"url"`
		Header   string `json:"header"`
		Editable bool   `json:"editable"`
		Scope    string `json:"scope"`
	} `json:"servers"`
}

// Adding an HTTP server via PUT /api/mcp persists it to mcp.json and keeps any
// stdio server already in the file; a URL-based server is editable, a stdio one is
// not. (The reconnect runs but the fake endpoints just fail to connect — the
// config persistence is what matters here.)
func TestMCPServersRoundTrip(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Seed a stdio server — it must survive a UI save.
	if err := mcp.SaveServers(filepath.Join(st.Root(), "mcp.json"), map[string]mcp.ServerConfig{
		"local": {Command: "nonexistent-mcp-cmd-xyz"},
	}); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	req, _ := http.NewRequest(http.MethodPut, api.URL+"/api/mcp",
		strings.NewReader(`{"servers":[{"name":"news","url":"https://news.example/mcp","header":"Bearer tok"}]}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT status = %d", resp.StatusCode)
	}

	var got mcpResp
	getJSONInto(t, api.URL+"/api/mcp", &got)
	type sv struct {
		url, header string
		editable    bool
	}
	byName := map[string]sv{}
	for _, s := range got.Servers {
		byName[s.Name] = sv{s.URL, s.Header, s.Editable}
	}
	if n, ok := byName["news"]; !ok || n.url != "https://news.example/mcp" || n.header != "Bearer tok" || !n.editable {
		t.Errorf("news server = %+v, want the http server editable", n)
	}
	if l, ok := byName["local"]; !ok || l.editable {
		t.Errorf("stdio 'local' = %+v, want preserved and not editable", l)
	}
}

// A project session's own .mcp.json servers are listed (scope "project",
// read-only) alongside the global ones, keyed by the conv query.
func TestProjectMCPServersListed(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projPath := t.TempDir()
	proj, err := st.OpenProject(projPath, "Proj")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.NewSession(proj.ID, "Sess")
	if err != nil {
		t.Fatal(err)
	}
	if err := mcp.SaveServers(filepath.Join(projPath, ".mcp.json"), map[string]mcp.ServerConfig{
		"proj-srv": {Command: "nonexistent-proj-cmd-xyz"},
	}); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(backend.New("http://unused.invalid"), st))
	t.Cleanup(api.Close)

	// No conv → only global servers (none configured here).
	var got mcpResp
	getJSONInto(t, api.URL+"/api/mcp", &got)
	if len(got.Servers) != 0 {
		t.Errorf("no-conv list = %d servers, want 0", len(got.Servers))
	}

	// With the project session, its server shows up — scope project, not editable.
	getJSONInto(t, api.URL+"/api/mcp?conv="+sess.ID, &got)
	found := false
	for _, s := range got.Servers {
		if s.Name == "proj-srv" {
			found = true
			if s.Scope != "project" || s.Editable {
				t.Errorf("proj-srv = %+v, want scope project and not editable", s)
			}
		}
	}
	if !found {
		t.Error("proj-srv (from the project's .mcp.json) should be listed for the project session")
	}
}

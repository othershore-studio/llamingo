// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

func TestChatAgainstFake(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))

	var out, errOut bytes.Buffer
	if err := chat(context.Background(), be, srv.URL, "", "Hi", &out, &errOut); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(out.String()); got != "Hello! How can I help you today?" {
		t.Errorf("reply = %q", got)
	}
	// The budget line reports the authoritative count from the stream's usage.
	if !strings.Contains(errOut.String(), "context 12/8192") {
		t.Errorf("stderr missing authoritative budget line:\n%s", errOut.String())
	}
}

// With no -url, the active saved connection (shared with the desktop) is used in full —
// its URL, token, persona, and sampling — so `llamingo` needs no flags. An explicit flag
// overrides, and overriding the URL drops the config's persona/sampling (a different
// endpoint was asked for). Env vars are cleared so the dev's own LLAMINGO_URL doesn't leak in.
func TestResolveConnection(t *testing.T) {
	t.Setenv("LLAMINGO_URL", "")
	t.Setenv("LLAMINGO_TOKEN", "")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.2
	if err := st.SetConfig(store.Config{URL: "http://lab:8080", Token: "sek", SystemPrompt: "Be terse.", Temperature: &temp}); err != nil {
		t.Fatal(err)
	}

	c := resolveConnection(st, "", false, "", false, "", false)
	if c.url != "http://lab:8080" || c.token != "sek" || c.system != "Be terse." {
		t.Errorf("config connection = %+v, want lab url/token/persona", c)
	}
	if c.temperature == nil || *c.temperature != 0.2 {
		t.Errorf("temperature = %v, want 0.2", c.temperature)
	}

	// Explicit -url overrides and drops the config persona + sampling.
	c = resolveConnection(st, "http://other:9090", true, "", false, "", false)
	if c.url != "http://other:9090" || c.system != "" || c.temperature != nil {
		t.Errorf("overridden = %+v, want a bare other:9090", c)
	}

	// Explicit -system wins over the connection's persona.
	if c := resolveConnection(st, "", false, "", false, "Custom.", true); c.system != "Custom." {
		t.Errorf("system = %q, want the flag", c.system)
	}
}

func TestResolveConnectionNoConfig(t *testing.T) {
	t.Setenv("LLAMINGO_URL", "")
	t.Setenv("LLAMINGO_TOKEN", "")
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := resolveConnection(st, "", false, "", false, "", false); c.url != "http://localhost:8080" {
		t.Errorf("url = %q, want the localhost default", c.url)
	}
}

func TestModelName(t *testing.T) {
	cases := map[string]string{
		`models/gemma-3-12b-it-Q4_K_M.gguf`:      "gemma-3-12b-it-Q4_K_M",
		`C:\models\qwen2.5-7b-instruct-q5.gguf`:  "qwen2.5-7b-instruct-q5",
		"":                                       "unknown model",
	}
	for in, want := range cases {
		if got := modelName(in); got != want {
			t.Errorf("modelName(%q) = %q, want %q", in, got, want)
		}
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/store"
)

// upsertConn edits in place by id, or appends a genuinely new connection.
func TestUpsertConn(t *testing.T) {
	conns := []store.Connection{{ID: "a", Name: "A", URL: "http://a"}}

	conns = upsertConn(conns, store.Connection{ID: "a", Name: "A2", URL: "http://a2"})
	if len(conns) != 1 || conns[0].Name != "A2" || conns[0].URL != "http://a2" {
		t.Fatalf("update by id: got %+v", conns)
	}

	conns = upsertConn(conns, store.Connection{ID: "b", Name: "B", URL: "http://b"})
	if len(conns) != 2 || conns[1].ID != "b" {
		t.Fatalf("append new: got %+v", conns)
	}
}

// removeConn drops the matching id and leaves the rest in order; a missing id is a no-op.
func TestRemoveConn(t *testing.T) {
	conns := []store.Connection{{ID: "a"}, {ID: "b"}, {ID: "c"}}

	conns = removeConn(conns, "b")
	if len(conns) != 2 || conns[0].ID != "a" || conns[1].ID != "c" {
		t.Fatalf("remove middle: got %+v", conns)
	}

	conns = removeConn(conns, "z")
	if len(conns) != 2 {
		t.Fatalf("remove missing should be a no-op: got %+v", conns)
	}
}

// applyActive mirrors every field (and the id) into the top-level config the backend
// reads — the same contract as the desktop's /activate.
func TestApplyActive(t *testing.T) {
	temp, minp := 0.2, 0.05
	c := store.Connection{ID: "lab", Name: "Lab", URL: "http://lab:8080", Token: "tok", Temperature: &temp, MinP: &minp, SystemPrompt: "be terse"}

	cfg := applyActive(store.Config{URL: "http://old", ActiveID: "old"}, c)
	if cfg.ActiveID != "lab" || cfg.URL != "http://lab:8080" || cfg.Token != "tok" {
		t.Fatalf("id/url/token: got %+v", cfg)
	}
	if cfg.Temperature == nil || *cfg.Temperature != 0.2 || cfg.MinP == nil || *cfg.MinP != 0.05 {
		t.Fatalf("sampling not mirrored: temp=%v minp=%v", cfg.Temperature, cfg.MinP)
	}
	if cfg.SystemPrompt != "be terse" {
		t.Fatalf("system prompt not mirrored: %q", cfg.SystemPrompt)
	}
}

// parseFloatPtr: blank is "unset" (nil, ok); a number is its pointer; junk is rejected.
func TestParseFloatPtr(t *testing.T) {
	if p, ok := parseFloatPtr("  "); !ok || p != nil {
		t.Errorf("blank: want nil,true; got %v,%v", p, ok)
	}
	if p, ok := parseFloatPtr("0.7"); !ok || p == nil || *p != 0.7 {
		t.Errorf("valid: got %v,%v", p, ok)
	}
	if _, ok := parseFloatPtr("abc"); ok {
		t.Errorf("junk: want ok=false")
	}
}

// floatStr round-trips with parseFloatPtr: nil -> "" and a value -> its text.
func TestFloatStr(t *testing.T) {
	if s := floatStr(nil); s != "" {
		t.Errorf("nil: want empty, got %q", s)
	}
	v := 0.7
	if s := floatStr(&v); s != "0.7" {
		t.Errorf("0.7: got %q", s)
	}
}

func TestConnName(t *testing.T) {
	cases := map[string]string{
		"http://localhost:8080/": "localhost:8080",
		"https://lab.example:443": "lab.example:443",
		"http://host":             "host",
		"":                        "",
	}
	for url, want := range cases {
		if got := connName(url); got != want {
			t.Errorf("connName(%q) = %q, want %q", url, got, want)
		}
	}
}

// newConnID is 8 random bytes as hex (16 chars) and not constant.
func TestNewConnID(t *testing.T) {
	a, b := newConnID(), newConnID()
	if len(a) != 16 {
		t.Errorf("len = %d, want 16", len(a))
	}
	if a == b {
		t.Errorf("two ids collided: %q", a)
	}
}

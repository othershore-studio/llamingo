// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store_test

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/store"
)

func TestPrefsRoundTrip(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A store with no prefs.json yields the zero Prefs, not an error.
	if p, err := s.Prefs(); err != nil || p.Location != "" || p.SystemPrompt != "" {
		t.Fatalf("empty store Prefs = %+v, err = %v; want zero", p, err)
	}

	if err := s.SetPrefs(store.Prefs{Location: "Quito, EC", SystemPrompt: "Be concise."}); err != nil {
		t.Fatal(err)
	}
	p, err := s.Prefs()
	if err != nil {
		t.Fatal(err)
	}
	if p.Location != "Quito, EC" || p.SystemPrompt != "Be concise." {
		t.Fatalf("Prefs = %+v", p)
	}
}

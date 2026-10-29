// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import "testing"

// The friendly model name prefers a real --alias and otherwise falls back to the
// model file's base name, ignoring an alias that just echoes the path.
func TestModelDisplayName(t *testing.T) {
	cases := []struct{ path, alias, want string }{
		{"/models/gemma-3-12b-it-Q4_K_M.gguf", "Gemma 3 12B", "Gemma 3 12B"}, // friendly alias wins
		{"/models/gemma-3-12b-it-Q4_K_M.gguf", "", "gemma-3-12b-it-Q4_K_M"},  // no alias → base name
		{"/models/x.gguf", "/models/x.gguf", "x"},                           // alias echoes the path → base name
		{"/models/x.gguf", "x.gguf", "x"},                                   // path-like alias → base name
		{"models/x.gguf", "x", "x"},                                         // alias equals base name
	}
	for _, c := range cases {
		if got := modelDisplayName(c.path, c.alias); got != c.want {
			t.Errorf("modelDisplayName(%q, %q) = %q, want %q", c.path, c.alias, got, c.want)
		}
	}
}

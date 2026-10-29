// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package backend_test

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
)

func TestSetConnection(t *testing.T) {
	be := backend.New("http://old:1234")
	if got := be.BaseURL(); got != "http://old:1234" {
		t.Fatalf("initial BaseURL = %q", got)
	}
	be.SetConnection("http://new:5678/", "tok") // trailing slash should be trimmed
	if got := be.BaseURL(); got != "http://new:5678" {
		t.Errorf("BaseURL after SetConnection = %q, want http://new:5678", got)
	}
}

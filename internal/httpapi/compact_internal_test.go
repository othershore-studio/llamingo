// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/contextbudget"
)

func TestShouldAutoCompact(t *testing.T) {
	if shouldAutoCompact(contextbudget.Budget{Estimate: 100, NCtx: 8192}) {
		t.Error("a healthy budget should not auto-compact")
	}
	if shouldAutoCompact(contextbudget.Budget{Estimate: 6500, NCtx: 8192}) {
		t.Error("a near (not full) budget should not auto-compact")
	}
	if !shouldAutoCompact(contextbudget.Budget{Estimate: 7500, NCtx: 8192}) {
		t.Error("a full budget should auto-compact")
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package localtools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCurrentTime(t *testing.T) {
	// A fixed instant in a −05:00 zone (Ecuador), so the rendering is deterministic.
	fixed := time.Date(2026, 10, 29, 16, 30, 45, 0, time.FixedZone("ECT", -5*3600))
	tool := &Tool{now: func() time.Time { return fixed }}

	defs := tool.Tools()
	if len(defs) != 1 || defs[0].Name != toolCurrentTime {
		t.Fatalf("tools = %+v, want one %q", defs, toolCurrentTime)
	}

	out, err := tool.Call(context.Background(), toolCurrentTime, "{}")
	if err != nil {
		t.Fatal(err)
	}
	// Local wall clock with offset, and the matching UTC instant.
	for _, want := range []string{"Thursday, 29 October 2026, 16:30:45 -05:00", "UTC 2026-10-29 21:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("currentTime = %q, want it to contain %q", out, want)
		}
	}

	if _, err := tool.Call(context.Background(), "no_such_tool", "{}"); err == nil {
		t.Error("an unknown tool name should error")
	}
}

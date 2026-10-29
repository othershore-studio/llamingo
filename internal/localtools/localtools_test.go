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
	if len(defs) != 2 || defs[0].Name != toolCurrentTime {
		t.Fatalf("tools = %+v, want get_current_time first of two", defs)
	}

	out, err := tool.Call(context.Background(), toolCurrentTime, "{}")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Thursday, 29 October 2026, 16:30:45 -05:00", "UTC 2026-10-29 21:30"} {
		if !strings.Contains(out, want) {
			t.Errorf("currentTime = %q, want it to contain %q", out, want)
		}
	}

	if _, err := tool.Call(context.Background(), "no_such_tool", "{}"); err == nil {
		t.Error("an unknown tool name should error")
	}
}

func TestGetLocation(t *testing.T) {
	// A clock in a named (IANA-style) zone, so the hint path is testable, and one
	// in Go's Windows-style "Local" zone, where there is no place name.
	named := func() time.Time {
		return time.Date(2026, 10, 29, 16, 0, 0, 0, time.FixedZone("America/Guayaquil", -5*3600))
	}
	local := func() time.Time {
		return time.Date(2026, 10, 29, 16, 0, 0, 0, time.FixedZone("Local", -5*3600))
	}

	// A configured location is returned verbatim (trimmed).
	set := &Tool{now: local, location: func() string { return "  Quito, Ecuador " }}
	if out, _ := set.Call(context.Background(), toolGetLocation, "{}"); !strings.Contains(out, "Quito, Ecuador") {
		t.Errorf("get_location (set) = %q, want it to contain the location", out)
	}

	// Unset, with a timezone that names a city → the city is offered as a hint.
	hinted := &Tool{now: named, location: func() string { return "" }}
	out, _ := hinted.Call(context.Background(), toolGetLocation, "{}")
	if !strings.Contains(out, "No location is set") || !strings.Contains(out, "Guayaquil") {
		t.Errorf("get_location (unset, named tz) = %q, want 'No location is set' and hint 'Guayaquil'", out)
	}

	// Unset, Windows-style zone (offset only) → no hint, just the ask.
	bare := &Tool{now: local, location: func() string { return "" }}
	out, _ = bare.Call(context.Background(), toolGetLocation, "{}")
	if !strings.Contains(out, "No location is set") {
		t.Errorf("get_location (unset, no tz name) = %q, want the ask message", out)
	}
	if strings.Contains(out, "hint") {
		t.Errorf("get_location (unset, no tz name) = %q, should offer no hint", out)
	}

	// A nil accessor is treated as no location (no panic).
	nilLoc := &Tool{now: local}
	if out, _ := nilLoc.Call(context.Background(), toolGetLocation, "{}"); !strings.Contains(out, "No location is set") {
		t.Errorf("get_location (nil accessor) = %q, want the ask message", out)
	}
}

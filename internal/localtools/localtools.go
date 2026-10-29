// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package localtools holds small native tools that give the model ambient,
// client-side context it otherwise lacks — the current time and the user's
// location. Like memtool and imagegen, the bundle satisfies the MCP loop's
// Registry, so it is offered to the model alongside the MCP servers. These tools
// are read-only and side-effect-free, so they skip the permission gate. Project
// file tools join here as they land.
package localtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// Tool is the bundle of local ambient tools.
type Tool struct {
	now      func() time.Time // injectable so tests are deterministic
	location func() string    // the user's configured location ("" = none)
}

// NewTool returns the local tools: the system clock, and the user's location
// read through the given accessor (a nil accessor is treated as "no location").
func NewTool(location func() string) *Tool {
	return &Tool{now: time.Now, location: location}
}

const (
	toolCurrentTime = "get_current_time"
	toolGetLocation = "get_location"
)

// Tools advertises the local tools to the model.
func (t *Tool) Tools() []backend.ToolDef {
	return []backend.ToolDef{
		{
			Name: toolCurrentTime,
			Description: "Get the current local date and time. Use it for anything time-relative " +
				"(today's date, the day of the week, how recent something is) — your training has a " +
				"cutoff and does not know the current date.",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		},
		{
			Name: toolGetLocation,
			Description: "Get the user's location (their city or country), which they set in Settings. " +
				"Use it for place-dependent things like local weather instead of assuming or asking every " +
				"turn. If no location is set, this tells you to ask the user.",
			Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
		},
	}
}

// Call dispatches a local tool by name.
func (t *Tool) Call(ctx context.Context, name, arguments string) (string, error) {
	switch name {
	case toolCurrentTime:
		return t.currentTime(), nil
	case toolGetLocation:
		return t.getLocation(), nil
	default:
		return "", fmt.Errorf("localtools: unknown tool %q", name)
	}
}

// currentTime renders the local wall clock with its UTC offset, plus the UTC
// instant — unambiguous for the model regardless of where it thinks it is.
func (t *Tool) currentTime() string {
	tm := t.now()
	return fmt.Sprintf("Current date and time: %s (UTC %s).",
		tm.Format("Monday, 2 January 2006, 15:04:05 -07:00"),
		tm.UTC().Format("2006-01-02 15:04"))
}

// getLocation returns the user's configured location. We never infer it
// automatically: on Windows the system exposes only a UTC offset (no place
// name), and OS geolocation would mean coordinates plus an external
// reverse-geocode, against the local-first design. So location is a manual
// setting; when it is unset ("empty means deny"), we tell the model to ask the
// user, offering a hint only where the platform actually names a place.
//
// Deferred automatic path, if ever pursued: take OS coordinates, round them so
// the exact point is not revealed, and snap to the nearest entry in a bundled
// big-cities-and-coordinates table — an offline reverse-geocode, before any
// external dependency.
func (t *Tool) getLocation() string {
	loc := ""
	if t.location != nil {
		loc = strings.TrimSpace(t.location())
	}
	if loc != "" {
		return "The user's location is: " + loc + "."
	}
	if hint := timezoneCityHint(t.now); hint != "" {
		return "No location is set. Ask the user where they are (they can save it in Settings so you " +
			"will not need to ask again). A possible hint from the system timezone: " + hint + "."
	}
	return "No location is set. Ask the user where they are so you can help with place-specific things; " +
		"they can save it in Settings so you will not need to ask again."
}

// timezoneCityHint tries to name a place from the system timezone. On Linux and
// macOS the zone is often an IANA name like "America/Guayaquil", whose last
// element is a representative city; on Windows it is just "Local" (offset only),
// so there is no hint.
func timezoneCityHint(now func() time.Time) string {
	name := now().Location().String()
	if name == "" || name == "Local" || name == "UTC" {
		return ""
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		return strings.ReplaceAll(name[i+1:], "_", " ")
	}
	return ""
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package localtools holds small native tools that give the model ambient,
// client-side context it otherwise lacks — starting with the current time. Like
// memtool and imagegen, the bundle satisfies the MCP loop's Registry, so it is
// offered to the model alongside the MCP servers. These tools are read-only and
// side-effect-free, so they skip the permission gate. Location and project file
// tools join here as they land.
package localtools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/othershore-studio/llamingo/internal/backend"
)

// Tool is the bundle of local ambient tools.
type Tool struct {
	now func() time.Time // injectable so tests are deterministic
}

// NewTool returns the local tools, reading the real system clock.
func NewTool() *Tool { return &Tool{now: time.Now} }

const toolCurrentTime = "get_current_time"

// Tools advertises the local tools to the model.
func (t *Tool) Tools() []backend.ToolDef {
	return []backend.ToolDef{{
		Name: toolCurrentTime,
		Description: "Get the current local date and time. Use it for anything time-relative " +
			"(today's date, the day of the week, how recent something is) — your training has a " +
			"cutoff and does not know the current date.",
		Parameters: json.RawMessage(`{"type":"object","properties":{}}`),
	}}
}

// Call dispatches a local tool by name.
func (t *Tool) Call(ctx context.Context, name, arguments string) (string, error) {
	switch name {
	case toolCurrentTime:
		return t.currentTime(), nil
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

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package autostart manages whether llaminGO launches at user login, and whether
// that login launch starts hidden in the system tray. The mechanism is per-OS and
// only Windows is implemented so far (a per-user registry Run entry); other builds
// report the feature unsupported so the UI hides it and the HTTP API still compiles
// cross-platform. State lives in the OS, not in a config file — the registry entry
// is the single source of truth, so nothing can drift out of sync with it.
package autostart

import (
	"errors"
	"strings"
)

// MinimizedFlag is the command-line flag the login launch carries so the app knows
// to start minimized (hidden in the tray). The autostart command embeds it (see
// buildCommand); main.go checks os.Args for it. It matches Margin's --minimized so
// the convention is the same across tools. Kept here so the one string has one home.
const MinimizedFlag = "--minimized"

// valueName is the Run entry's name under the key — stable across launches so the
// app can find and remove its own entry.
const valueName = "llaminGO"

// State is the current login-autostart configuration.
type State struct {
	Supported bool `json:"supported"` // whether this build can manage autostart at all
	Enabled   bool `json:"enabled"`   // llaminGO launches at login
	Minimized bool `json:"minimized"` // the login launch starts hidden in the tray
}

// ErrUnsupported is returned by Enable/Disable on a platform with no autostart
// implementation. Status reports Supported:false there instead of erroring, so the
// UI can simply omit the controls.
var ErrUnsupported = errors.New("autostart: not supported on this platform")

// buildCommand is the Run entry's command: the quoted executable path, plus the
// tray flag when the login launch should start minimized.
func buildCommand(exe string, minimized bool) string {
	cmd := `"` + exe + `"`
	if minimized {
		cmd += " " + MinimizedFlag
	}
	return cmd
}

// isMinimized reports whether a stored Run command starts minimized.
func isMinimized(command string) bool {
	return strings.Contains(command, MinimizedFlag)
}

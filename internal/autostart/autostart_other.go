// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !windows

package autostart

// Autostart is only implemented on Windows so far. On other platforms the feature
// reports unsupported (so the UI omits the controls) and the setters fail loudly
// rather than pretending to have done something. macOS (a LaunchAgent plist) and
// Linux (an XDG .desktop autostart file) are the natural next implementations.

// Status reports the feature unsupported on this platform.
func Status() (State, error) { return State{Supported: false}, nil }

// Enable is unsupported on this platform.
func Enable(minimized bool) error { return ErrUnsupported }

// Disable is unsupported on this platform.
func Disable() error { return ErrUnsupported }

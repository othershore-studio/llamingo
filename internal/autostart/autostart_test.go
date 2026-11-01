// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package autostart

import (
	"strings"
	"testing"
)

// The command the Run entry stores: the quoted exe, with the tray flag only when
// the login launch should start minimized — and isMinimized reads it back. This is
// the logic main.go's flag check mirrors, so it runs on every platform (including
// CI), independent of the OS-specific registry wiring.
func TestBuildAndReadCommand(t *testing.T) {
	const exe = `C:\Program Files\llaminGO\llamingo.exe`

	plain := buildCommand(exe, false)
	if plain != `"`+exe+`"` {
		t.Errorf("plain command = %q, want the quoted exe alone", plain)
	}
	if isMinimized(plain) {
		t.Error("a plain command must not read as minimized")
	}

	hidden := buildCommand(exe, true)
	if !strings.HasPrefix(hidden, `"`+exe+`"`) || !strings.Contains(hidden, MinimizedFlag) {
		t.Errorf("minimized command = %q, want the quoted exe plus %s", hidden, MinimizedFlag)
	}
	if !isMinimized(hidden) {
		t.Error("a minimized command must read as minimized")
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build windows

package winsetup

import "testing"

func TestPathHas(t *testing.T) {
	p := `C:\Windows;C:\tools\llamingo;C:\Users\x\bin`
	if !pathHas(p, `C:\tools\llamingo`) {
		t.Error("exact entry not found")
	}
	if !pathHas(p, `C:\tools\llamingo\`) {
		t.Error("trailing slash should match")
	}
	if !pathHas(p, `c:\TOOLS\LLAMINGO`) {
		t.Error("match should be case-insensitive")
	}
	if pathHas(p, `C:\tools\other`) {
		t.Error("unrelated dir matched")
	}
	if pathHas("", `C:\tools`) {
		t.Error("empty PATH matched")
	}
}

// menuCommand builds the command and exeFromCommand reads the binary back out of it; they
// round-trip, including a path with spaces and the -c (resume) variant.
func TestExeFromCommandRoundTrip(t *testing.T) {
	if got := exeFromCommand(menuCommand(`C:\tools\llamingo.exe`, false)); got != `C:\tools\llamingo.exe` {
		t.Errorf("open: got %q", got)
	}
	if got := exeFromCommand(menuCommand(`C:\a b\llamingo.exe`, true)); got != `C:\a b\llamingo.exe` {
		t.Errorf("resume with spaces: got %q", got)
	}
	if got := exeFromCommand("just some text"); got != "" {
		t.Errorf("non-command parsed to %q", got)
	}
}

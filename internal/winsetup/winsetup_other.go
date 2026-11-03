// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build !windows

package winsetup

import "errors"

// errUnsupported is returned by the setup operations off Windows, where PATH and shell
// integration are handled differently (on Unix, a symlink into ~/.local/bin or a shell rc
// line — a later addition). The caller guards on the OS, so these are never hit at runtime.
var errUnsupported = errors.New("winsetup: PATH / right-click setup is Windows-only for now")

// Status mirrors the Windows type so cross-platform callers compile; it reports "nothing to
// do" off Windows.
type Status struct {
	ExePath       string
	OnPath        bool
	MenuInstalled bool
	MenuTarget    string
}

func (Status) NeedsHeal() bool  { return false }
func (Status) Configured() bool { return true }

func Check(exePath string) (Status, error)  { return Status{ExePath: exePath}, nil }
func ApplyPath(exeDir string) (bool, error) { return false, errUnsupported }
func ApplyMenu(exePath string) error        { return errUnsupported }
func RemoveMenu() error                     { return errUnsupported }

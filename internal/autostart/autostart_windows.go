// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build windows

package autostart

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows/registry"
)

// runKeyPath is the per-user autostart key: entries here launch at login for the
// current user, no admin rights needed (the HKLM equivalent would).
const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// Status reports whether llaminGO is set to launch at login and, if so, whether it
// starts minimized. A missing key or value simply means "not enabled".
func Status() (State, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return State{Supported: true}, nil
		}
		return State{Supported: true}, fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()

	cmd, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return State{Supported: true}, nil
	}
	if err != nil {
		return State{Supported: true}, fmt.Errorf("autostart: read Run value: %w", err)
	}
	return State{Supported: true, Enabled: true, Minimized: isMinimized(cmd)}, nil
}

// Enable writes (or rewrites) the Run entry pointing at this executable, carrying
// the tray flag when the login launch should start minimized.
func Enable(minimized bool) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("autostart: locate executable: %w", err)
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	if err := k.SetStringValue(valueName, buildCommand(exe, minimized)); err != nil {
		return fmt.Errorf("autostart: write Run value: %w", err)
	}
	return nil
}

// Disable removes the Run entry. A missing entry is already "disabled", not an error.
func Disable() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("autostart: open Run key: %w", err)
	}
	defer k.Close()
	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("autostart: delete Run value: %w", err)
	}
	return nil
}

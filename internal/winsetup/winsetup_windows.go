// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package winsetup registers the terminal binary with Windows in the user's own context
// (no installer, no admin): it adds the binary's folder to the user PATH so `llamingo`
// runs from anywhere, and installs an Explorer right-click menu on folders — "Call a
// llaminGO here" and a "(resume)" variant. Everything is under HKEY_CURRENT_USER and is
// reversible. Because it records the current binary path, a moved binary is detected and
// can be healed by re-running setup. All of it lives in the user's own hive, matching the
// no-install, user-context posture.
package winsetup

import (
	"fmt"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	keyOpen     = `Software\Classes\Directory\shell\LlamingoCode`
	keyResume   = `Software\Classes\Directory\shell\LlamingoCodeResume`
	labelOpen   = "Call a llaminGO here"
	labelResume = "Call a llaminGO here (resume)"
)

// Status is what's currently registered for a given binary.
type Status struct {
	ExePath       string
	OnPath        bool   // the binary's folder is on the user PATH
	MenuInstalled bool   // the right-click menu exists
	MenuTarget    string // the binary the menu points at ("" if none)
}

// NeedsHeal is true when the menu exists but points at a different binary — the exe moved.
func (s Status) NeedsHeal() bool {
	return s.MenuInstalled && !strings.EqualFold(s.MenuTarget, s.ExePath)
}

// Configured is true when the PATH and the menu are both in place and point here.
func (s Status) Configured() bool { return s.OnPath && s.MenuInstalled && !s.NeedsHeal() }

// Check reports the current registration state for exePath.
func Check(exePath string) (Status, error) {
	exePath = filepath.Clean(exePath)
	s := Status{ExePath: exePath}
	if v, err := userPath(); err == nil {
		s.OnPath = pathHas(v, filepath.Dir(exePath))
	}
	if target, ok := menuTarget(); ok {
		s.MenuInstalled = true
		s.MenuTarget = target
	}
	return s, nil
}

// ApplyPath adds the binary's folder to the user PATH if it isn't already there, and tells
// running shells so new terminals see it. Reports whether it changed anything.
func ApplyPath(exeDir string) (bool, error) {
	exeDir = filepath.Clean(exeDir)
	cur, err := userPath()
	if err != nil {
		return false, err
	}
	if pathHas(cur, exeDir) {
		return false, nil
	}
	next := exeDir
	if strings.TrimSpace(cur) != "" {
		next = strings.TrimRight(cur, ";") + ";" + exeDir
	}
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.SET_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	if err := k.SetExpandStringValue("Path", next); err != nil {
		return false, err
	}
	broadcastEnv()
	return true, nil
}

// ApplyMenu installs (or repoints) the Explorer right-click entries to run this binary.
func ApplyMenu(exePath string) error {
	exePath = filepath.Clean(exePath)
	if err := writeMenu(keyOpen, labelOpen, menuCommand(exePath, false), exePath); err != nil {
		return err
	}
	return writeMenu(keyResume, labelResume, menuCommand(exePath, true), exePath)
}

// RemoveMenu removes the right-click entries. The PATH entry is left alone (harmless, and
// the user may rely on it); Check/ApplyPath manage it.
func RemoveMenu() error {
	_ = registry.DeleteKey(registry.CURRENT_USER, keyOpen+`\command`)
	_ = registry.DeleteKey(registry.CURRENT_USER, keyResume+`\command`)
	_ = registry.DeleteKey(registry.CURRENT_USER, keyOpen)
	_ = registry.DeleteKey(registry.CURRENT_USER, keyResume)
	return nil
}

func userPath() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, "Environment", registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()
	v, _, err := k.GetStringValue("Path")
	if err == registry.ErrNotExist {
		return "", nil
	}
	return v, err
}

// pathHas reports whether dir is one of the ;-separated entries in pathValue, comparing
// case-insensitively and ignoring trailing slashes (Windows path semantics).
func pathHas(pathValue, dir string) bool {
	want := strings.TrimRight(filepath.Clean(dir), `\`)
	for _, p := range strings.Split(pathValue, ";") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(filepath.Clean(p), `\`), want) {
			return true
		}
	}
	return false
}

// menuCommand is the Explorer command: open a new console in the clicked folder (%V)
// running the binary. cmd /c start is used so it works without Windows Terminal; on Win11
// the default-terminal handoff gives Windows Terminal anyway.
func menuCommand(exePath string, resume bool) string {
	c := fmt.Sprintf(`cmd.exe /c start "llaminGO" /D "%%V" "%s"`, exePath)
	if resume {
		c += " -c"
	}
	return c
}

// menuTarget reads the binary the "open here" command currently points at.
func menuTarget() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, keyOpen+`\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if err != nil {
		return "", false
	}
	return exeFromCommand(v), true
}

// exeFromCommand pulls the binary path out of a stored command string, which looks like
// `cmd.exe /c start "llaminGO" /D "%V" "<exe>"` (optionally followed by -c).
func exeFromCommand(cmd string) string {
	const anchor = `"%V" "`
	i := strings.Index(cmd, anchor)
	if i < 0 {
		return ""
	}
	rest := cmd[i+len(anchor):]
	if j := strings.IndexByte(rest, '"'); j >= 0 {
		return rest[:j]
	}
	return ""
}

func writeMenu(key, label, command, iconExe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, key, registry.WRITE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", label); err != nil {
		return err
	}
	// Icon points at the binary, so once it carries an embedded icon the menu shows it.
	if err := k.SetStringValue("Icon", iconExe); err != nil {
		return err
	}
	ck, _, err := registry.CreateKey(registry.CURRENT_USER, key+`\command`, registry.WRITE)
	if err != nil {
		return err
	}
	defer ck.Close()
	return ck.SetStringValue("", command)
}

// broadcastEnv notifies running processes that the environment changed, so new terminals
// pick up the PATH without a sign-out. Best effort — a failure just means "takes effect on
// next login."
func broadcastEnv() {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001a
		smtoAbortIfHung = 0x0002
	)
	env, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return
	}
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	_, _, _ = proc.Call(uintptr(hwndBroadcast), wmSettingChange, 0, uintptr(unsafe.Pointer(env)), smtoAbortIfHung, 5000, 0)
}

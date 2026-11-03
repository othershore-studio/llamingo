// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

// The Windows binary carries the llaminGO icon so Explorer, the taskbar, and the
// right-click menu (whose registry Icon value points at the binary — see
// internal/winsetup) all show the llama. On Windows the icon is an embedded resource the
// Go linker picks up automatically from a .syso file in this directory. That .syso is
// generated from the app icon and committed; `go build` embeds it for windows/amd64 (the
// _windows_amd64 filename suffix keeps it off every other target).
//
// Regenerate it after the icon changes with `go generate ./cmd/llamingo`:
//
//go:generate go run github.com/akavel/rsrc@v0.10.2 -ico ../../build/windows/icon.ico -arch amd64 -o rsrc_windows_amd64.syso

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package main

// The Windows taita.exe carries the taita icon (the hatted llama) so Explorer, the
// taskbar, and shortcuts show it. On Windows the icon is an embedded resource the Go
// linker picks up automatically from a .syso file in this directory. That .syso is
// generated from the app icon and committed; `go build` embeds it for windows/amd64 (the
// _windows_amd64 filename suffix keeps it off every other target).
//
// The icon itself comes from brand/llamingo-taita-logo.svg, rasterized (Inkscape) to
// 16/32/48/64/128/256 px PNGs and packed into build/windows/taita-icon.ico.
//
// Regenerate the .syso after the icon changes with `go generate ./cmd/taita`:
//
//go:generate go run github.com/akavel/rsrc@v0.10.2 -ico ../../build/windows/taita-icon.ico -arch amd64 -o rsrc_windows_amd64.syso

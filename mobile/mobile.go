// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package mobile is the gomobile entrypoint for the "keep Go" mobile path
// (Option A): it starts the llaminGO core as an in-process HTTP server on
// loopback, serving both the core API and the embedded Svelte UI, so a thin
// native shell (Android/iOS) only has to call Start and point a WebView at the
// returned localhost port. The native app can't exec a shipped binary, so the
// core runs *in* the app's process via a gomobile-bound .aar / .xcframework.
//
// The WebView loads http://127.0.0.1:<port>/ → the SPA (same origin), whose
// relative /api calls hit this same in-process server. No frontend change: a
// plain WebView has no window.runtime, so the UI's apiOrigin resolves to "".
package mobile

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

// web/dist is the built SPA. In CI it holds only its .gitignore (the web build
// runs after `go test`), which still satisfies the embed; a real bind embeds the
// full UI built just before `gomobile bind`.
//
//go:embed all:web/dist
var webAssets embed.FS

var listener net.Listener

// Start opens the store under dataDir (the app's private data dir), points the
// backend at llamaURL (empty = the saved config's URL), serves the API + UI on an
// OS-assigned loopback port, and returns that port for the WebView to load. Safe
// to call once per process.
func Start(dataDir, llamaURL string) (int, error) {
	if listener != nil {
		// Idempotent: the Activity can be recreated (rotation, return from
		// background) and call Start again — hand back the running port, don't error.
		return listener.Addr().(*net.TCPAddr).Port, nil
	}
	st, err := store.Open(dataDir)
	if err != nil {
		return 0, err
	}
	if llamaURL == "" {
		if cfg, cerr := st.Config(); cerr == nil {
			llamaURL = cfg.URL
		}
	}

	api := httpapi.New(backend.New(llamaURL), st)
	dist, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		return 0, fmt.Errorf("mobile: embedded UI: %w", err)
	}
	files := http.FileServer(http.FS(dist))
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0") // OS-assigned free loopback port
	if err != nil {
		return 0, err
	}
	listener = ln
	go func() { _ = http.Serve(ln, mux) }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

// Port returns the loopback port the core is serving on, or 0 if not started.
func Port() int {
	if listener == nil {
		return 0
	}
	return listener.Addr().(*net.TCPAddr).Port
}

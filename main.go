// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build windows

// Command llamingo is the Wails desktop shell: a native window whose webview
// loads the embedded Svelte SPA. The core's HTTP/SSE adapter runs on a loopback
// port that the webview calls directly — Wails' asset server cannot flush, so
// streaming (SSE) must come from a real net/http server. The UI is the same thin
// client of that API as in the browser and the v2 PWA. Windows-only for now; the
// build tag keeps the cross-platform `go test ./...` CI from compiling the GUI.
package main

import (
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

//go:embed all:web/dist
var assets embed.FS

// apiAddr is the loopback address the core API listens on inside the desktop
// app; the embedded webview fetches it directly (see web/src: window.runtime).
const apiAddr = "127.0.0.1:8766"

func main() {
	url := env("LLAMINGO_URL", "http://localhost:8080")
	var opts []backend.Option
	if tok := os.Getenv("LLAMINGO_TOKEN"); tok != "" {
		opts = append(opts, backend.WithToken(tok))
	}
	be := backend.New(url, opts...)

	st, err := store.Open(defaultDataDir())
	if err != nil {
		log.Fatal(err)
	}

	dist, err := fs.Sub(assets, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	// Serve the core API on loopback (a real server that streams); bind before
	// the window opens so the SPA's first request can't race the listener.
	ln, err := net.Listen("tcp", apiAddr)
	if err != nil {
		log.Fatal(err)
	}
	go func() { _ = http.Serve(ln, withCORS(httpapi.New(be, st))) }()

	if err := wails.Run(&options.App{
		Title:  "llaminGO",
		Width:  1100,
		Height: 720,
		AssetServer: &assetserver.Options{
			Assets: dist,
		},
	}); err != nil {
		log.Fatal(err)
	}
}

// withCORS lets the webview (a wails:// origin) call the loopback API.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".llamingo"
	}
	return filepath.Join(home, ".llamingo")
}

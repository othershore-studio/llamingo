// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

//go:build windows

// Command llamingo is the Wails desktop shell: a native window whose webview
// loads the embedded Svelte SPA, with the core's HTTP/SSE adapter mounted as
// the asset server's handler so /api/* is served same-origin. The UI is a thin
// client of that API — exactly as in the browser and the v2 PWA. Windows-only
// for now (the roadmap adds macOS/Linux later); the build tag keeps the
// cross-platform `go test ./...` CI from compiling the GUI dependencies.
package main

import (
	"embed"
	"io/fs"
	"log"
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

	if err := wails.Run(&options.App{
		Title:  "llaminGO",
		Width:  1100,
		Height: 720,
		AssetServer: &assetserver.Options{
			Assets:  dist,
			Handler: httpapi.New(be, st),
		},
	}); err != nil {
		log.Fatal(err)
	}
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

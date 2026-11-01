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
	"context"
	"embed"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"

	"github.com/energye/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/store"
)

//go:embed all:web/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

// apiAddr is the loopback address the core API listens on inside the desktop
// app; the embedded webview fetches it directly (see web/src: window.runtime).
const apiAddr = "127.0.0.1:8766"

// Close-to-tray state. Closing the window hides it to the system tray instead of
// quitting; the app really exits only via the tray's Quit, which sets quitting
// so OnBeforeClose lets the close through. Wails v2 has no native tray, so the
// icon/menu come from energye/systray (see docs/wails-v3 note: v3 is native).
var (
	appCtx   context.Context
	quitting bool
)

func main() {
	st, err := store.Open(defaultDataDir())
	if err != nil {
		log.Fatal(err)
	}

	// Resolve the connection: an env var wins (dev override), then the saved
	// config (so it survives across launches), then the default.
	cfg, _ := st.Config()
	url := firstNonEmpty(os.Getenv("LLAMINGO_URL"), cfg.URL, "http://localhost:8080")
	var opts []backend.Option
	if tok := firstNonEmpty(os.Getenv("LLAMINGO_TOKEN"), cfg.Token); tok != "" {
		opts = append(opts, backend.WithToken(tok))
	}
	be := backend.New(url, opts...)
	be.SetSampling(cfg.Temperature, cfg.MinP) // the active connection's sampling (nil = server default)

	dist, err := fs.Sub(assets, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	// Serve the core API on loopback (a real server that streams); bind before
	// the window opens so the SPA's first request can't race the listener. A busy
	// port almost always means a second instance is launching, so don't die here:
	// fall through to wails.Run, whose SingleInstanceLock hands off to the running
	// instance (surfacing its window) and exits this one.
	if ln, lnErr := net.Listen("tcp", apiAddr); lnErr == nil {
		go func() { _ = http.Serve(ln, withCORS(httpapi.New(be, st))) }()
	} else {
		log.Printf("API port %s busy (%v); deferring to the already-running instance", apiAddr, lnErr)
	}

	// Wails owns the main loop, so the tray runs via RunWithExternalLoop: start
	// it once the app is up (OnStartup) and stop it on shutdown.
	trayStart, trayEnd := systray.RunWithExternalLoop(onTrayReady, func() {})

	if err := wails.Run(&options.App{
		Title:     "llaminGO",
		Width:     1100,
		Height:    720,
		MinWidth:  600,
		MinHeight: 400,
		Frameless: true,
		AssetServer: &assetserver.Options{
			Assets: dist,
		},
		OnStartup: func(ctx context.Context) {
			appCtx = ctx
			trayStart()
			// The title-bar menu's "Close llaminGO" emits this for a real exit;
			// the window's X only hides to the tray. quitting lets OnBeforeClose pass.
			runtime.EventsOn(ctx, "app:quit", func(...interface{}) {
				quitting = true
				runtime.Quit(ctx)
			})
		},
		OnShutdown: func(ctx context.Context) {
			trayEnd()
		},
		OnBeforeClose: func(ctx context.Context) (prevent bool) {
			if quitting {
				return false // a real quit was requested from the tray
			}
			runtime.WindowHide(ctx) // close-to-tray: hide and keep running
			return true
		},
		// Single instance: a second launch exits and asks the running one to
		// surface its window (restoring it from the tray if it was hidden).
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "studio.othershore.llamingo",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				showWindow()
			},
		},
	}); err != nil {
		log.Fatal(err)
	}
}

// onTrayReady wires the tray icon and menu once the tray is up. A left-click (or
// "Show llaminGO") restores the window; "Quit" exits the app for real.
func onTrayReady() {
	systray.SetIcon(trayIcon)
	systray.SetTooltip("llaminGO")
	systray.SetOnClick(func(systray.IMenu) { showWindow() })
	mShow := systray.AddMenuItem("Show llaminGO", "Show the window")
	mShow.Click(showWindow)
	mQuit := systray.AddMenuItem("Quit", "Quit llaminGO")
	mQuit.Click(func() {
		quitting = true
		runtime.Quit(appCtx)
	})
}

// showWindow brings the window back from the tray.
func showWindow() {
	if appCtx == nil {
		return
	}
	runtime.WindowShow(appCtx)
	runtime.WindowUnminimise(appCtx)
}

// withCORS lets the webview (a wails:// origin) call the loopback API.
func withCORS(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		// List every method the API uses (see internal/httpapi routes) — the
		// webview is cross-origin, so a method missing here fails its preflight.
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "content-type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// firstNonEmpty returns the first non-empty string, or "" if all are empty.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func defaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".llamingo"
	}
	return filepath.Join(home, ".llamingo")
}

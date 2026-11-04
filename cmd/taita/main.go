// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command taita is the llaminGO multi-user gateway: one process that serves the
// embedded chat-only web UI and the core HTTP/SSE API, giving each browser
// session its own private store (an anonymous cookie now; named accounts later).
// It is what you point a browser — and, later, a client — at over your LAN or a
// tunnel. One llama backend and one image-generation queue are shared across all
// users. See internal/taita and the memory note taita-build-plan.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
	"github.com/othershore-studio/llamingo/internal/taita"
)

// web/dist is the built SPA, staged here by cmd/taita/build.sh because go:embed
// cannot reach ../../web/dist. On a fresh checkout the directory holds only its
// .gitignore, which still satisfies the embed; a real build copies the UI in.
//
//go:embed all:web/dist
var webAssets embed.FS

func main() {
	// `taita admin ...` is a console toolkit for recovering the admin account
	// (reassign, reset a password) without the web UI — run on the server box,
	// where shell access is the proof of ownership. It needs no running server.
	if len(os.Args) > 1 && os.Args[1] == "admin" {
		runAdmin(os.Args[2:])
		return
	}

	addr := flag.String("addr", ":8788", "listen address")
	url := flag.String("url", "", "llama-server base URL (default: $LLAMINGO_URL, then http://localhost:8080)")
	token := flag.String("token", "", "optional bearer token (default: $LLAMINGO_TOKEN)")
	dataFlag := flag.String("data", "", "per-user store root (default: $TAITA_DATA, then ~/.taita/data)")
	configFlag := flag.String("config", "", "server config dir (default: $TAITA_CONFIG, then ~/.taita/config); reserved until centralized config lands")
	fake := flag.Bool("fake", false, "serve against an in-process fake llama-server (demo/preview; no model needed)")
	adminFlag := flag.String("admin", "", "designate the admin account by name (default: $TAITA_ADMIN); empty = the first account to sign in")
	flag.Parse()

	dataDir := firstNonEmpty(*dataFlag, os.Getenv("TAITA_DATA"), defaultDir("data"))
	configDir := firstNonEmpty(*configFlag, os.Getenv("TAITA_CONFIG"), defaultDir("config"))
	adminName := firstNonEmpty(*adminFlag, os.Getenv("TAITA_ADMIN"))

	var be backend.Backend
	if *fake {
		srv := lcpptest.NewServer()
		log.Printf("backend: in-process FAKE llama-server at %s", srv.URL)
		be = backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	} else {
		// Adopt the admin's saved connection (set in the Settings UI) as the backend,
		// so pointing taita at a llama-server sticks across restarts. Order: an
		// explicit -url / $LLAMINGO_URL wins, then the saved connection, then the
		// localhost default. Same for the token.
		savedURL, savedToken := savedConnection(configDir)
		resolved := firstNonEmpty(*url, os.Getenv("LLAMINGO_URL"), savedURL, "http://localhost:8080")
		var opts []backend.Option
		if tok := firstNonEmpty(*token, os.Getenv("LLAMINGO_TOKEN"), savedToken); tok != "" {
			opts = append(opts, backend.WithToken(tok))
		}
		log.Printf("backend: llama-server at %s", resolved)
		be = backend.New(resolved, opts...)
	}

	dist, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		log.Fatalf("taita: embedded UI: %v", err)
	}

	gw := taita.New(be, dataDir, dist, taita.WithAdmin(adminName), taita.WithConfigDir(configDir))
	who := adminName
	if who == "" {
		who = "first to sign in"
	}
	log.Printf("taita listening on %s (data: %s, config: %s; admin: %s)", browseURL(*addr), dataDir, configDir, who)
	log.Fatal(http.ListenAndServe(*addr, gw))
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

// savedConnection returns the backend url + token the admin configured through the
// Settings UI, read from the central config store: the connection recorded active
// if there is one, else the first saved connection (configured but not yet
// activated — e.g. added and saved without clicking the radio). Empty when nothing
// is saved, so the caller falls back to the localhost default.
func savedConnection(configDir string) (url, token string) {
	cs, err := store.Open(configDir)
	if err != nil {
		return "", ""
	}
	c, err := cs.Config()
	if err != nil {
		return "", ""
	}
	if c.URL != "" {
		return c.URL, c.Token
	}
	if len(c.Connections) > 0 {
		return c.Connections[0].URL, c.Connections[0].Token
	}
	return "", ""
}

// browseURL turns a listen address into a URL a person can open in a browser. A
// bare ":8788" (all interfaces) becomes http://localhost:8788 — the common local
// case — so the startup line reads as a clickable address rather than a bare port
// that gets mistaken for the backend's URL above it.
func browseURL(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	return "http://" + addr
}

// defaultDir is ~/.taita/<sub>, or .taita/<sub> relative if the home dir is unknown.
func defaultDir(sub string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".taita", sub)
	}
	return filepath.Join(home, ".taita", sub)
}

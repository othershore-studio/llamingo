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

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/taita"
)

// web/dist is the built SPA, staged here by cmd/taita/build.sh because go:embed
// cannot reach ../../web/dist. On a fresh checkout the directory holds only its
// .gitignore, which still satisfies the embed; a real build copies the UI in.
//
//go:embed all:web/dist
var webAssets embed.FS

func main() {
	addr := flag.String("addr", ":8788", "listen address")
	url := flag.String("url", "", "llama-server base URL (default: $LLAMINGO_URL, then http://localhost:8080)")
	token := flag.String("token", "", "optional bearer token (default: $LLAMINGO_TOKEN)")
	dataFlag := flag.String("data", "", "per-user store root (default: $TAITA_DATA, then ~/.taita/data)")
	configFlag := flag.String("config", "", "server config dir (default: $TAITA_CONFIG, then ~/.taita/config); reserved until centralized config lands")
	fake := flag.Bool("fake", false, "serve against an in-process fake llama-server (demo/preview; no model needed)")
	flag.Parse()

	dataDir := firstNonEmpty(*dataFlag, os.Getenv("TAITA_DATA"), defaultDir("data"))
	configDir := firstNonEmpty(*configFlag, os.Getenv("TAITA_CONFIG"), defaultDir("config"))

	var be backend.Backend
	if *fake {
		srv := lcpptest.NewServer()
		log.Printf("backend: in-process FAKE llama-server at %s", srv.URL)
		be = backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	} else {
		// The -url flag wins, then the env var, then the default. Taita's own
		// centralized connection config (config/) is a later increment; for now the
		// one shared backend comes from the flag/env.
		resolved := firstNonEmpty(*url, os.Getenv("LLAMINGO_URL"), "http://localhost:8080")
		var opts []backend.Option
		if tok := firstNonEmpty(*token, os.Getenv("LLAMINGO_TOKEN")); tok != "" {
			opts = append(opts, backend.WithToken(tok))
		}
		log.Printf("backend: llama-server at %s", resolved)
		be = backend.New(resolved, opts...)
	}

	dist, err := fs.Sub(webAssets, "web/dist")
	if err != nil {
		log.Fatalf("taita: embedded UI: %v", err)
	}

	gw := taita.New(be, dataDir, dist)
	log.Printf("taita listening on %s (data: %s, config: %s [reserved])", *addr, dataDir, configDir)
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

// defaultDir is ~/.taita/<sub>, or .taita/<sub> relative if the home dir is unknown.
func defaultDir(sub string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".taita", sub)
	}
	return filepath.Join(home, ".taita", sub)
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Command llamingo-serve runs the HTTP/SSE adapter over the core — the server
// the web UI (and, later, the Wails app and the v2 gateway) talks to. It serves
// against a real llama-server by default; -fake uses an in-process fake so the
// UI can be previewed or demoed without a model.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "listen address")
	url := flag.String("url", "", "llama-server base URL (default: $LLAMINGO_URL, then saved config, then http://localhost:8080)")
	token := flag.String("token", "", "optional bearer token (default: $LLAMINGO_TOKEN or saved config)")
	data := flag.String("data", defaultDataDir(), "store root directory")
	fake := flag.Bool("fake", false, "serve against an in-process fake llama-server (demo/preview; no model needed)")
	flag.Parse()

	st, err := store.Open(*data)
	if err != nil {
		log.Fatal(err)
	}

	var be backend.Backend
	if *fake {
		srv := lcpptest.NewServer()
		log.Printf("backend: in-process FAKE llama-server at %s", srv.URL)
		be = backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	} else {
		// The -url flag wins, then the env var, then the saved config, then the
		// default — so a persisted connection survives without re-passing a flag.
		cfg, _ := st.Config()
		resolved := firstNonEmpty(*url, os.Getenv("LLAMINGO_URL"), cfg.URL, "http://localhost:8080")
		var opts []backend.Option
		if tok := firstNonEmpty(*token, os.Getenv("LLAMINGO_TOKEN"), cfg.Token); tok != "" {
			opts = append(opts, backend.WithToken(tok))
		}
		log.Printf("backend: llama-server at %s", resolved)
		be = backend.New(resolved, opts...)
	}

	log.Printf("llaminGO adapter listening on %s (store: %s)", *addr, *data)
	log.Fatal(http.ListenAndServe(*addr, httpapi.New(be, st)))
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

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
	url := flag.String("url", env("LLAMINGO_URL", "http://localhost:8080"), "llama-server base URL")
	token := flag.String("token", os.Getenv("LLAMINGO_TOKEN"), "optional bearer token for an auth-fronted proxy")
	data := flag.String("data", defaultDataDir(), "store root directory")
	fake := flag.Bool("fake", false, "serve against an in-process fake llama-server (demo/preview; no model needed)")
	flag.Parse()

	var be backend.Backend
	if *fake {
		srv := lcpptest.NewServer()
		log.Printf("backend: in-process FAKE llama-server at %s", srv.URL)
		be = backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	} else {
		var opts []backend.Option
		if *token != "" {
			opts = append(opts, backend.WithToken(*token))
		}
		log.Printf("backend: llama-server at %s", *url)
		be = backend.New(*url, opts...)
	}

	st, err := store.Open(*data)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("llaminGO adapter listening on %s (store: %s)", *addr, *data)
	log.Fatal(http.ListenAndServe(*addr, httpapi.New(be, st)))
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

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import "net/http"

// CORS gates the loopback core API for the desktop webview, which calls it from a
// different origin than the one it was served from. It allowlists the webview's own
// origin(s) and REJECTS any other browser origin with 403 — so a web page you
// happen to visit while llaminGO is running cannot read the API (your backend
// token, your chat history) or drive it with a forged cross-site request, because
// the browser always attaches an Origin header that page scripts cannot forge.
//
// Requests with no Origin (the CLI, the smoke test, any non-browser caller) pass
// through untouched: a local non-browser process is a separate, higher-privilege
// threat this does not try to stop. The caller supplies the trusted origins (the
// desktop passes the Wails webview origins); with none, every browser origin is
// rejected and only Origin-less callers get through.
func CORS(next http.Handler, allowedOrigins ...string) http.Handler {
	allowed := make(map[string]bool, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" {
			if !allowed[origin] {
				http.Error(w, "origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "content-type")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

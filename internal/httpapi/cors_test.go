// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/othershore-studio/llamingo/internal/httpapi"
)

// The CORS gate lets the webview's own origin through (echoing it back), rejects
// every other browser origin with 403 before the handler runs (so a drive-by can
// neither read a response nor forge a write), and leaves Origin-less callers alone.
func TestCORSOriginAllowlist(t *testing.T) {
	const good = "http://wails.localhost"
	var reached bool
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	h := httpapi.CORS(next, good)

	do := func(method, origin string) *httptest.ResponseRecorder {
		reached = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/settings", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		h.ServeHTTP(rec, req)
		return rec
	}

	// Allowed origin: passes through, ACAO echoes it.
	if rec := do(http.MethodGet, good); !reached || rec.Code != http.StatusOK {
		t.Fatalf("allowed origin: reached=%v code=%d", reached, rec.Code)
	} else if got := rec.Header().Get("Access-Control-Allow-Origin"); got != good {
		t.Errorf("ACAO = %q, want %q", got, good)
	}

	// Disallowed origin: 403, handler not reached, no ACAO (read attack blocked).
	if rec := do(http.MethodGet, "https://evil.example"); reached || rec.Code != http.StatusForbidden {
		t.Errorf("disallowed GET: reached=%v code=%d, want blocked 403", reached, rec.Code)
	} else if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("disallowed origin got an ACAO header")
	}

	// A cross-site POST from a bad origin is blocked before the handler (CSRF write).
	if rec := do(http.MethodPost, "https://evil.example"); reached || rec.Code != http.StatusForbidden {
		t.Errorf("disallowed POST: reached=%v code=%d, want blocked 403", reached, rec.Code)
	}

	// No Origin (CLI / smoke test / non-browser): passes through, no ACAO added.
	if rec := do(http.MethodGet, ""); !reached || rec.Code != http.StatusOK {
		t.Errorf("no-origin request: reached=%v code=%d", reached, rec.Code)
	} else if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("no-origin request got an ACAO header")
	}

	// OPTIONS preflight from the allowed origin: 204 with CORS headers, no handler.
	if rec := do(http.MethodOptions, good); reached || rec.Code != http.StatusNoContent {
		t.Errorf("preflight: reached=%v code=%d, want 204 without reaching handler", reached, rec.Code)
	} else if rec.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("preflight missing Access-Control-Allow-Methods")
	}
}

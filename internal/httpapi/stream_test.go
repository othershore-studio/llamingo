// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/httpapi"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

// The chat stream must tell an nginx-family reverse proxy (Nginx Proxy Manager /
// openresty) not to buffer it: SSE has to stream, or a buffered + idle stream —
// e.g. one paused on a permission prompt — is dropped on the proxy's read-timeout,
// taking the pending permission with it (the later "allow" then 404s). Regression
// guard for that bug; see docs/taita.md (reverse-proxy deployment).
func TestChatStreamDisablesProxyBuffering(t *testing.T) {
	llama := lcpptest.NewServer()
	t.Cleanup(llama.Close)
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpapi.New(be, st))
	t.Cleanup(api.Close)

	id := createChat(t, api, "Chat")

	resp, err := http.Post(api.URL+"/api/chat/stream", "application/json",
		strings.NewReader(`{"id":"`+id+`","content":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := resp.Header.Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want %q (SSE must not be proxy-buffered)", got, "no")
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so the handler goroutine finishes
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package fetchtool_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/fetchtool"
	"github.com/othershore-studio/llamingo/internal/permission"
)

// recordingPrompter captures the request it was asked about and answers with a
// fixed decision, so a test can both control the gate and inspect what the user
// would see (the per-host ToolID, the URL in Detail).
type recordingPrompter struct {
	allow bool
	asked int
	last  permission.Request
}

func (p *recordingPrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	p.asked++
	p.last = req
	return permission.Decision{Allow: p.allow, Scope: permission.ScopeSession}, nil
}

func newTool(allow bool, opts ...fetchtool.Option) (*fetchtool.Tool, *recordingPrompter) {
	p := &recordingPrompter{allow: allow}
	gate := permission.New(p, permission.NewMemoryGrantStore())
	return fetchtool.NewTool(gate, opts...), p
}

func TestFetchHappyPath(t *testing.T) {
	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, "<html><body>hello world</body></html>")
	}))
	defer srv.Close()

	// Inject the server's client so the fetch reaches loopback (the SSRF guard
	// would otherwise, correctly, refuse 127.0.0.1 — tested separately below).
	tool, p := newTool(true, fetchtool.WithHTTPClient(srv.Client()))
	out, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("result missing body: %q", out)
	}
	if !strings.Contains(out, "Fetched "+srv.URL) {
		t.Errorf("result missing the honest header: %q", out)
	}
	if !strings.Contains(gotUA, "llaminGO") {
		t.Errorf("User-Agent = %q, want it to name llaminGO", gotUA)
	}
	if p.asked != 1 {
		t.Errorf("gate asked %d times, want 1", p.asked)
	}
	if !strings.HasPrefix(p.last.ToolID, "native:fetch:") {
		t.Errorf("gate ToolID = %q, want per-host native:fetch:<host>", p.last.ToolID)
	}
	if !strings.Contains(p.last.Detail, srv.URL) {
		t.Errorf("gate Detail = %q, want it to show the exact URL", p.last.Detail)
	}
}

func TestSchemeRejectedBeforeGate(t *testing.T) {
	tool, p := newTool(true)
	if _, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"file:///etc/passwd"}`); err == nil {
		t.Error("want an error for a non-http scheme")
	}
	if p.asked != 0 {
		t.Errorf("gate asked for a rejected scheme (asked=%d) — the scheme check must precede the gate", p.asked)
	}
}

// A denial is surfaced as a result, not an error, and never touches the network.
func TestDenialIsResultNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a denied fetch must not hit the network")
	}))
	defer srv.Close()
	tool, _ := newTool(false, fetchtool.WithHTTPClient(srv.Client()))
	out, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("a refusal must not be an error: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "declined") {
		t.Errorf("denial result = %q, want a clear 'declined' message", out)
	}
}

func TestNon2xxSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	tool, _ := newTool(true, fetchtool.WithHTTPClient(srv.Client()))
	if _, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`); err == nil {
		t.Error("want an error for a 404")
	}
}

func TestSizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, strings.Repeat("A", 4096))
	}))
	defer srv.Close()
	tool, _ := newTool(true, fetchtool.WithHTTPClient(srv.Client()), fetchtool.WithMaxBytes(100))
	out, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out, "A"); n > 100 {
		t.Errorf("body not capped: got %d 'A's, want <= 100", n)
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("want a truncation marker in the header: %q", out)
	}
}

// The default client is SSRF-guarded: httptest serves on 127.0.0.1, so a real
// fetch of it must be refused at dial time — the guard that stops a model (or a
// redirect) from reaching internal services, even for an approved host.
func TestSSRFGuardBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the SSRF guard must refuse loopback before any request is served")
	}))
	defer srv.Close()
	tool, _ := newTool(true) // default guarded client, no WithHTTPClient
	_, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err == nil {
		t.Fatal("want a blocked-address error fetching loopback")
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Errorf("error = %v, want a non-public-address refusal", err)
	}
}

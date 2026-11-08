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
)

// A big page's extracted text is capped to the configured limit, with a note, so
// one fetch can't swamp a small context on the post-fetch round.
func TestOutputCapTruncates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("word ", 5000)) // 25,000 chars
	}))
	defer srv.Close()
	tool, _ := newTool(true, fetchtool.WithHTTPClient(srv.Client()), fetchtool.WithMaxOutput(100))
	out, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "truncated to the configured 100-character limit") {
		t.Errorf("want an output-cap note; got:\n%s", out)
	}
	if n := strings.Count(out, "word"); n > 25 {
		t.Errorf("output not capped: %d 'word's, want <= 25", n)
	}
}

// A non-positive cap means no limit — a large-context model gets the whole page.
func TestOutputCapUnlimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, strings.Repeat("word ", 5000))
	}))
	defer srv.Close()
	tool, _ := newTool(true, fetchtool.WithHTTPClient(srv.Client()), fetchtool.WithMaxOutput(-1))
	out, err := tool.Call(context.Background(), fetchtool.ToolName, `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "truncated to the configured") {
		t.Error("WithMaxOutput(-1) should not cap the output")
	}
	if n := strings.Count(out, "word"); n < 4000 {
		t.Errorf("unlimited output seems capped: only %d 'word's", n)
	}
}

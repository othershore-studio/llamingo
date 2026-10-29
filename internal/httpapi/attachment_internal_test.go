// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/store"
)

// A remote image a tool put in an answer is fetched, stored under the
// conversation's attachments/, and its link rewritten to the local ref; the
// serve route then hands the bytes back. A link that fails to download is left
// untouched so the message is never broken.
func TestImageAttachments(t *testing.T) {
	const png = "\x89PNG\r\n\x1a\nFAKEBYTES"
	served := 0
	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gone") {
			http.Error(w, "expired", http.StatusNotFound)
			return
		}
		served++
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, png)
	}))
	defer img.Close()

	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.NewChat("Pics")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(nil, st) // the backend is unused by the attachment path

	content := "Here:\n\n![weather](" + img.URL + "/img/tok.png)\n\nand dead ![x](" + img.URL + "/gone.png)"
	got := srv.imageAttachments(context.Background(), c.ID, content)

	// The fresh link was localized; the failed one was left as-is; one fetch each.
	if !strings.Contains(got, "](attachments/") {
		t.Fatalf("live image not rewritten to a local ref:\n%s", got)
	}
	if !strings.Contains(got, img.URL+"/gone.png") {
		t.Errorf("a failed download should leave the original link:\n%s", got)
	}
	if served != 1 {
		t.Errorf("successful image fetched %d times, want 1", served)
	}

	// Pull the rewritten ref and fetch it back through the serve route.
	rest := got[strings.Index(got, "attachments/"):]
	name := strings.TrimPrefix(rest[:strings.IndexByte(rest, ')')], "attachments/")

	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/attachments/"+name, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("serve attachment: status %d", rec.Code)
	}
	if rec.Body.String() != png {
		t.Errorf("served bytes = %q, want the saved PNG", rec.Body.String())
	}

	// A missing name is a clean 404, not a path escape.
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/api/conversations/"+c.ID+"/attachments/missing.png", nil))
	if rec2.Code != http.StatusNotFound {
		t.Errorf("missing attachment: status %d, want 404", rec2.Code)
	}
}

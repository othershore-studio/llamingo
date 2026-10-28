// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

func TestSegmentsForIncludesMemory(t *testing.T) {
	llama := lcpptest.NewServer()
	defer llama.Close()
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(be, st)
	msgs := []backend.Message{{Role: backend.RoleUser, Content: "hi"}}

	// Without memory: just the conversation segment.
	if segs := srv.segmentsFor(msgs); len(segs) != 1 || segs[0].Name != "conversation" {
		t.Fatalf("no-memory segments = %+v, want [conversation]", segs)
	}

	// With memory: a metered "memory" segment precedes the conversation.
	if err := st.WriteMemory("remember this"); err != nil {
		t.Fatal(err)
	}
	segs := srv.segmentsFor(msgs)
	if len(segs) != 2 || segs[0].Name != "memory" || segs[1].Name != "conversation" {
		t.Fatalf("segments = %+v, want [memory conversation]", segs)
	}
}

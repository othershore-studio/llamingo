// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"path/filepath"
	"strings"
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
	if segs := srv.segmentsFor("", msgs, nil); len(segs) != 1 || segs[0].Name != "conversation" {
		t.Fatalf("no-memory segments = %+v, want [conversation]", segs)
	}

	// With memory: a metered "memory" segment precedes the conversation.
	if err := st.WriteMemory("remember this"); err != nil {
		t.Fatal(err)
	}
	segs := srv.segmentsFor("", msgs, nil)
	if len(segs) != 2 || segs[0].Name != "memory" || segs[1].Name != "conversation" {
		t.Fatalf("segments = %+v, want [memory conversation]", segs)
	}
}

// Offered tools are their own metered segment (a system message of their
// schemas), so the budget counts the tool cost — which is a real chunk.
func TestSegmentsForIncludesTools(t *testing.T) {
	llama := lcpptest.NewServer()
	defer llama.Close()
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(be, st)
	msgs := []backend.Message{{Role: backend.RoleUser, Content: "hi"}}
	tools := []backend.ToolDef{{Name: "write_memory", Description: "save a note"}}
	segs := srv.segmentsFor("", msgs, tools)
	if len(segs) != 2 || segs[0].Name != "tools" || segs[1].Name != "conversation" {
		t.Fatalf("segments = %+v, want [tools conversation]", segs)
	}
}

// The active connection's system prompt is its own metered segment, prepended
// before the conversation (a persona's instructions reach the model each turn).
func TestSegmentsForIncludesSystemPrompt(t *testing.T) {
	llama := lcpptest.NewServer()
	defer llama.Close()
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfig(store.Config{SystemPrompt: "You are a terse coding assistant."}); err != nil {
		t.Fatal(err)
	}
	srv := New(be, st)
	segs := srv.segmentsFor("", []backend.Message{{Role: backend.RoleUser, Content: "hi"}}, nil)
	if len(segs) != 2 || segs[0].Name != "system" || segs[1].Name != "conversation" {
		t.Fatalf("segments = %+v, want [system conversation]", segs)
	}
}

// A project session prepends a metered "workdir" note naming its working folder
// (so the model uses relative paths); a project can opt the note out, and a free
// chat never gets one.
func TestSegmentsForWorkingDir(t *testing.T) {
	llama := lcpptest.NewServer()
	defer llama.Close()
	be := backend.New(llama.URL, backend.WithHTTPClient(llama.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "proj")
	p, err := st.OpenProject(work, "Proj")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := st.NewSession(p.ID, "S")
	if err != nil {
		t.Fatal(err)
	}
	srv := New(be, st)
	msgs := []backend.Message{{Role: backend.RoleUser, Content: "hi"}}

	segs := srv.segmentsFor(sess.ID, msgs, nil)
	if len(segs) != 2 || segs[0].Name != "workdir" || segs[1].Name != "conversation" {
		t.Fatalf("project segments = %+v, want [workdir conversation]", segs)
	}
	if c := segs[0].Messages[0].Content; !strings.Contains(c, work) || !strings.Contains(c, "relative to it") {
		t.Errorf("workdir note = %q, want it to name %q and say paths are relative", c, work)
	}

	// Opting out suppresses the note.
	if err := st.SetProjectShowWorkingDir(work, false); err != nil {
		t.Fatal(err)
	}
	if segs := srv.segmentsFor(sess.ID, msgs, nil); len(segs) != 1 || segs[0].Name != "conversation" {
		t.Fatalf("opted-out segments = %+v, want [conversation]", segs)
	}

	// A free chat gets no workdir note.
	free, err := st.NewChat("Free")
	if err != nil {
		t.Fatal(err)
	}
	if segs := srv.segmentsFor(free.ID, msgs, nil); len(segs) != 1 || segs[0].Name != "conversation" {
		t.Fatalf("free-chat segments = %+v, want [conversation]", segs)
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/store"
)

// A turn against the fake streams the reply, holds the exchange in memory, persists
// both sides to the store, and reports the authoritative budget — the floor working
// CLI-direct over the core, no HTTP.
func TestSessionTurnAgainstFake(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	var streamed strings.Builder
	if err := sess.Turn(context.Background(), "Hi", func(d backend.Delta) error {
		streamed.WriteString(d.Content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(streamed.String()); got != "Hello! How can I help you today?" {
		t.Errorf("streamed reply = %q", got)
	}
	// Held in memory: user + assistant.
	if len(sess.msgs) != 2 || sess.msgs[0].Role != backend.RoleUser || sess.msgs[1].Role != backend.RoleAssistant {
		t.Fatalf("messages = %+v, want user + assistant", sess.msgs)
	}
	if !strings.Contains(sess.msgs[1].Content, "Hello") {
		t.Errorf("assistant message = %q", sess.msgs[1].Content)
	}
	// Persisted to the store (survives a reopen): just the two messages, no tool trail.
	entries, err := st.LoadConversation(sess.convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Role != "user" || entries[1].Role != "assistant" {
		t.Fatalf("persisted entries = %+v, want user + assistant", entries)
	}
	// The authoritative budget comes back from the stream's usage.
	if b := sess.Budget(); b.Authoritative != 12 {
		t.Errorf("budget authoritative = %d, want 12", b.Authoritative)
	}
}

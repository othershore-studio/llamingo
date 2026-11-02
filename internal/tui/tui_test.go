// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
)

// allowPrompter approves any tool request. The floor test uses no tools, so it just
// satisfies the gate's prompter.
type allowPrompter struct{}

func (allowPrompter) Ask(context.Context, permission.Request, []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: true, Scope: permission.ScopeOnce}, nil
}

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
	sess, err := Open(be, st, t.TempDir(), allowPrompter{})
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
	var user, asst int
	for _, e := range entries {
		switch e.Role {
		case "user":
			user++
		case "assistant":
			asst++
		}
	}
	if user != 1 || asst != 1 {
		t.Fatalf("persisted messages: user=%d assistant=%d, want 1 each; entries=%+v", user, asst, entries)
	}
	// Via mcp.Run the model's reasoning is captured as a display-only trail entry.
	if len(entries) < 3 {
		t.Errorf("expected a reasoning trail entry too; got %d entries", len(entries))
	}
	// The authoritative budget comes back from the stream's usage.
	if b := sess.Budget(); b.Authoritative != 12 {
		t.Errorf("budget authoritative = %d, want 12", b.Authoritative)
	}
}

// The /tools view toggles a tool's curation per-chat: it persists to the store and the
// turn's registry is curated fresh from that set, so a disabled tool is no longer
// offered to the model. This drives the same Session accessors the view does, without
// the UI.
func TestSessionToolCuration(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{})
	if err != nil {
		t.Fatal(err)
	}

	tools := sess.Tools()
	if len(tools) == 0 {
		t.Fatal("expected native tools in the base registry")
	}
	first := tools[0].Name

	// A fresh chat curates nothing off — the whole set is offered.
	if d := sess.disabledSet(); len(d) != 0 {
		t.Fatalf("fresh chat disabled set = %v, want empty", d)
	}

	// Toggle one off: now-disabled, persisted, and dropped from the curated registry a
	// turn would use.
	nowDisabled, err := sess.ToggleTool(first)
	if err != nil {
		t.Fatal(err)
	}
	if !nowDisabled {
		t.Fatalf("ToggleTool(%q) = false, want now-disabled", first)
	}
	if d := sess.disabledSet(); !d[first] {
		t.Errorf("disabled set %v missing %q after toggle", d, first)
	}
	curated := mcp.Curate(sess.reg, sess.disabledSet()).Tools()
	if len(curated) != len(tools)-1 {
		t.Errorf("curated tools = %d, want %d", len(curated), len(tools)-1)
	}
	for _, tl := range curated {
		if tl.Name == first {
			t.Errorf("curated registry still offers disabled tool %q", first)
		}
	}

	// Persisted to meta.json (survives a reopen).
	got, err := st.ConversationTools(sess.convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != first {
		t.Errorf("persisted disabled = %v, want [%q]", got, first)
	}

	// Toggle back on: the set empties.
	nowDisabled, err = sess.ToggleTool(first)
	if err != nil {
		t.Fatal(err)
	}
	if nowDisabled {
		t.Errorf("second ToggleTool(%q) = true, want now-enabled", first)
	}
	if d := sess.disabledSet(); len(d) != 0 {
		t.Errorf("disabled set %v, want empty after toggling back", d)
	}
}

// A terminal turn gets the same system framing as the desktop: the working-directory
// note always (so the model knows its cwd), the personal memory once saved (so it can
// recall it — the write path alone never fed memory back), and a persona first when set.
func TestSessionSystemSegments(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{})
	if err != nil {
		t.Fatal(err)
	}

	// Fresh project session: the working-directory note, nothing else.
	names := segNames(sess.systemSegments())
	if !contains(names, "workdir") {
		t.Errorf("system segments %v missing workdir", names)
	}
	if contains(names, "memory") || contains(names, "system") {
		t.Errorf("unexpected segments %v for a fresh session", names)
	}

	// Saved memory is fed back into the prompt (the recall path, not just the write).
	if err := st.WriteMemory("Daniel prefers tea."); err != nil {
		t.Fatal(err)
	}
	if names := segNames(sess.systemSegments()); !contains(names, "memory") {
		t.Errorf("system segments %v missing memory after WriteMemory", names)
	}

	// A persona (-system) leads the framing.
	sess.system = "You are terse."
	segs := sess.systemSegments()
	if len(segs) == 0 || segs[0].Name != "system" {
		t.Fatalf("segments = %v, want system first", segNames(segs))
	}
	if !strings.Contains(segs[0].Messages[0].Content, "terse") {
		t.Errorf("system segment = %q", segs[0].Messages[0].Content)
	}
}

func segNames(segs []contextbudget.Segment) []string {
	n := make([]string, len(segs))
	for i, s := range segs {
		n[i] = s.Name
	}
	return n
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

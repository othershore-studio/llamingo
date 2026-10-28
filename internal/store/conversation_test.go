// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"path/filepath"
	"testing"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// The on-disk log round-trips exactly: parse ∘ marshal is the identity on the
// serialized form, including a summary entry and a body with a blank line.
func TestConversationRoundTrip(t *testing.T) {
	entries := []Entry{
		{ID: "0a1f", Kind: KindMessage, Role: "user", Time: mustTime("2026-10-28T14:02:11Z"), Content: "What's a good 3-day Kyoto itinerary?"},
		{ID: "0a20", Kind: KindMessage, Role: "assistant", Time: mustTime("2026-10-28T14:02:19Z"), Content: "Day 1: Fushimi Inari.\n\nDay 2: Arashiyama."},
		{ID: "0a90", Kind: KindSummary, Time: mustTime("2026-10-28T14:40:00Z"), CoversFrom: "0a1f", CoversTo: "0a20", Model: "gemma-3-12b", Replaced: 7240, SummaryTokens: 190, Content: "Planned a 3-day Kyoto trip."},
	}
	data := marshalEntries("Trip planning", entries)

	title, got := parseConversation(data)
	if title != "Trip planning" {
		t.Fatalf("title = %q, want %q", title, "Trip planning")
	}
	if len(got) != 3 {
		t.Fatalf("parsed %d entries, want 3:\n%s", len(got), data)
	}
	if string(marshalEntries(title, got)) != string(data) {
		t.Errorf("round-trip mismatch:\n--- got ---\n%s\n--- want ---\n%s", marshalEntries(title, got), data)
	}
	if got[1].Content != "Day 1: Fushimi Inari.\n\nDay 2: Arashiyama." {
		t.Errorf("entry 1 body (with blank line) not preserved: %q", got[1].Content)
	}
	if got[2].Kind != KindSummary || got[2].CoversFrom != "0a1f" || got[2].CoversTo != "0a20" || got[2].Replaced != 7240 {
		t.Errorf("summary entry not parsed: %+v", got[2])
	}
}

func TestAppendAndLoad(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Greetings")
	if err != nil {
		t.Fatal(err)
	}

	u, err := s.AppendMessage(c.ID, "user", "Hello there")
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != "user" || u.Kind != KindMessage || u.ID == "" || u.Time.IsZero() {
		t.Fatalf("bad appended entry: %+v", u)
	}
	if _, err := s.AppendMessage(c.ID, "assistant", "General Kenobi!"); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d entries, want 2", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "Hello there" {
		t.Errorf("entry 0 = %+v", got[0])
	}
	if got[1].Role != "assistant" || got[1].Content != "General Kenobi!" {
		t.Errorf("entry 1 = %+v", got[1])
	}
	if got[0].ID == got[1].ID {
		t.Errorf("entry ids collided: %s", got[0].ID)
	}
}

// A conversation id resolves whether it is a free chat or a project session.
func TestAppendToSession(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.OpenProject(filepath.Join(t.TempDir(), "work"), "Work")
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.NewSession(p.ID, "Session one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(sess.ID, "user", "inside a session"); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadConversation(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Content != "inside a session" {
		t.Fatalf("session load = %+v", got)
	}
}

func TestLoadUnknownConversation(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadConversation("deadbeefdeadbeef"); err == nil {
		t.Fatal("expected error loading an unknown conversation")
	}
}

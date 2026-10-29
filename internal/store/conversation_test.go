// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"path/filepath"
	"strings"
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

// The marshaled log shows a visible speaker label before each entry (so a
// Markdown viewer reveals who said what), while bodies still round-trip.
func TestVisibleSpeakerLabels(t *testing.T) {
	data := marshalEntries("Chat", []Entry{
		{ID: "a1", Kind: KindMessage, Role: "user", Time: mustTime("2026-10-28T14:02:11Z"), Content: "hi"},
		{ID: "a2", Kind: KindMessage, Role: "assistant", Time: mustTime("2026-10-28T14:02:19Z"), Content: "hello"},
	})
	for _, want := range []string{"**You**", "**Assistant**"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("marshaled log is missing the visible label %q:\n%s", want, data)
		}
	}
	// The label sits on the marker line, not in the body.
	_, got := parseConversation(data)
	if len(got) != 2 || got[0].Content != "hi" || got[1].Content != "hello" {
		t.Fatalf("bodies not preserved past the labels: %+v", got)
	}
}

// Logs written in the pre-label format (a bare marker at the start of the line)
// still parse — the speaker label is optional on read.
func TestParseLegacyFormat(t *testing.T) {
	legacy := "# Old chat\n\n<!-- llamingo:msg id=a1 role=user t=2026-10-28T14:02:11Z -->\nhi\n\n<!-- llamingo:msg id=a2 role=assistant t=2026-10-28T14:02:19Z -->\nhello\n"
	_, got := parseConversation([]byte(legacy))
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "hi" || got[1].Role != "assistant" || got[1].Content != "hello" {
		t.Fatalf("legacy format did not parse: %+v", got)
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

func TestAppendSummary(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Chat")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.AppendMessage(c.ID, "user", "one")
	b, _ := s.AppendMessage(c.ID, "assistant", "two")

	if _, err := s.AppendSummary(c.ID, SummaryInput{
		CoversFrom: a.ID, CoversTo: b.ID, Model: "m", Content: "folded", Replaced: 10, SummaryTokens: 3,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3 (2 msgs + 1 summary): %+v", len(got), got)
	}
	sum := got[2]
	if sum.Kind != KindSummary || sum.CoversFrom != a.ID || sum.CoversTo != b.ID || sum.Content != "folded" {
		t.Errorf("summary entry = %+v", sum)
	}
}

// Project substitutes the active summary for the messages it shadows.
func TestProject(t *testing.T) {
	msgs := []Entry{
		{ID: "m1", Kind: KindMessage, Role: "user", Content: "1"},
		{ID: "m2", Kind: KindMessage, Role: "assistant", Content: "2"},
		{ID: "m3", Kind: KindMessage, Role: "user", Content: "3"},
		{ID: "m4", Kind: KindMessage, Role: "assistant", Content: "4"},
	}

	// No summary → messages unchanged.
	if p := Project(msgs); len(p) != 4 {
		t.Fatalf("no-summary projection = %d entries, want 4", len(p))
	}

	// A summary covering m1..m2 → [summary, m3, m4] (the summary stands first).
	s1 := Entry{ID: "s1", Kind: KindSummary, CoversFrom: "m1", CoversTo: "m2", Content: "folded"}
	proj := Project(append(append([]Entry{}, msgs...), s1))
	if len(proj) != 3 || proj[0].Kind != KindSummary || proj[1].ID != "m3" || proj[2].ID != "m4" {
		t.Fatalf("projection = %+v, want [summary, m3, m4]", proj)
	}

	// A later, larger summary wins over the earlier one.
	s2 := Entry{ID: "s2", Kind: KindSummary, CoversFrom: "m1", CoversTo: "m3", Content: "folded more"}
	proj2 := Project(append(append([]Entry{}, msgs...), s1, s2))
	if len(proj2) != 2 || proj2[0].ID != "s2" || proj2[1].ID != "m4" {
		t.Fatalf("projection2 = %+v, want [s2, m4]", proj2)
	}
}

// AppendAssistant stores the reasoning trace as its own entry before the answer;
// Project keeps it out of the prompt, so the trace is display-only.
func TestAppendAssistantWithReasoning(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Thinking")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendMessage(c.ID, "user", "remember I like tea"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistant(c.ID, "Noted!", "The user wants a preference saved. I'll call write_memory."); err != nil {
		t.Fatal(err)
	}

	got, err := s.LoadConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("loaded %d entries, want 3 (user, reasoning, assistant): %+v", len(got), got)
	}
	if got[1].Kind != KindReasoning || got[1].Content != "The user wants a preference saved. I'll call write_memory." {
		t.Errorf("reasoning entry = %+v", got[1])
	}
	if got[2].Kind != KindMessage || got[2].Role != "assistant" || got[2].Content != "Noted!" {
		t.Errorf("assistant entry = %+v", got[2])
	}

	// Display-only: the projection the model sees has only the two messages.
	proj := Project(got)
	if len(proj) != 2 || proj[0].Role != "user" || proj[1].Role != "assistant" {
		t.Fatalf("projection = %+v, want [user, assistant] (no reasoning)", proj)
	}
	for _, e := range proj {
		if e.Kind == KindReasoning {
			t.Error("reasoning leaked into the projection sent to the model")
		}
	}
}

// An assistant turn without a trace stores just the answer — no empty reasoning entry.
func TestAppendAssistantNoReasoning(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendAssistant(c.ID, "Hi!", "   "); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadConversation(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindMessage || got[0].Content != "Hi!" {
		t.Fatalf("want a single assistant message, got %+v", got)
	}
}

// A reasoning entry round-trips through the on-disk format like any other entry.
func TestReasoningRoundTrip(t *testing.T) {
	entries := []Entry{
		{ID: "0b01", Kind: KindMessage, Role: "user", Time: mustTime("2026-10-28T14:02:11Z"), Content: "hi"},
		{ID: "0b02", Kind: KindReasoning, Time: mustTime("2026-10-28T14:02:14Z"), Content: "They greeted me.\nI'll answer warmly."},
		{ID: "0b03", Kind: KindMessage, Role: "assistant", Time: mustTime("2026-10-28T14:02:19Z"), Content: "Hello!"},
	}
	data := marshalEntries("Chat", entries)

	_, got := parseConversation(data)
	if len(got) != 3 || got[1].Kind != KindReasoning || got[1].Content != "They greeted me.\nI'll answer warmly." {
		t.Fatalf("reasoning did not round-trip: %+v\n%s", got, data)
	}
	if string(marshalEntries("Chat", got)) != string(data) {
		t.Errorf("round-trip mismatch:\n%s", data)
	}
}

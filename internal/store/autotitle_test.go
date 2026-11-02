// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"strings"
	"testing"
)

func TestDeriveTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"How do I reverse a linked list in Go?", "How do I reverse a linked list in Go?"},
		{"  leading and trailing spaces  ", "leading and trailing spaces"},
		{"first line\nsecond line", "first line"},
		{"\n\n  skip blank lines\nthen this", "skip blank lines"},
		{"", ""},
		{"   \n  \t ", ""},
	}
	for _, c := range cases {
		if got := deriveTitle(c.in); got != c.want {
			t.Errorf("deriveTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// A long message truncates with an ellipsis, stays within the limit, and never
	// splits a word — the cut lands on a word boundary.
	orig := "one two three four five six seven eight nine ten eleven twelve thirteen fourteen"
	long := deriveTitle(orig)
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("long title should end with an ellipsis: %q", long)
	}
	if n := len([]rune(long)); n > 49 {
		t.Errorf("long title = %d runes, want <= 49: %q", n, long)
	}
	head := strings.TrimSuffix(long, "…")
	if !strings.HasPrefix(orig, head) {
		t.Fatalf("truncation isn't a prefix of the message: %q", head)
	}
	if orig[len(head)] != ' ' {
		t.Errorf("truncation split a word: %q (next char %q)", head, string(orig[len(head)]))
	}

	// A single over-long token (no boundary to fall back to) is hard-cut, not kept whole.
	if n := len([]rune(deriveTitle(strings.Repeat("x", 100)))); n > 49 {
		t.Errorf("over-long token not cut: %d runes", n)
	}
}

func TestMaybeAutoTitle(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	title := func(id string) string {
		chats, err := s.ListChats()
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range chats {
			if c.ID == id {
				return c.Title
			}
		}
		t.Fatalf("chat %s not found", id)
		return ""
	}

	// The first message names a default-titled chat.
	c, _ := s.NewChat(DefaultChatTitle)
	if err := s.MaybeAutoTitle(c.ID, "What's a good 3-day Kyoto itinerary?"); err != nil {
		t.Fatal(err)
	}
	if got := title(c.ID); got != "What's a good 3-day Kyoto itinerary?" {
		t.Errorf("auto-title = %q, want the first message", got)
	}
	// A later message does not re-title (the title is no longer a default).
	if err := s.MaybeAutoTitle(c.ID, "and the weather?"); err != nil {
		t.Fatal(err)
	}
	if got := title(c.ID); got != "What's a good 3-day Kyoto itinerary?" {
		t.Errorf("re-titled on a later message: %q", got)
	}

	// A user rename is never clobbered.
	c2, _ := s.NewChat(DefaultChatTitle)
	if err := s.Rename(c2.ID, "My own name"); err != nil {
		t.Fatal(err)
	}
	if err := s.MaybeAutoTitle(c2.ID, "some first message"); err != nil {
		t.Fatal(err)
	}
	if got := title(c2.ID); got != "My own name" {
		t.Errorf("clobbered a user rename: %q", got)
	}

	// An all-blank first message keeps the default.
	c3, _ := s.NewChat(DefaultChatTitle)
	if err := s.MaybeAutoTitle(c3.ID, "  \n\t "); err != nil {
		t.Fatal(err)
	}
	if got := title(c3.ID); got != DefaultChatTitle {
		t.Errorf("blank message changed the title to %q, want the default", got)
	}

	// The session default is recognized too.
	c4, _ := s.NewChat(DefaultSessionTitle)
	if err := s.MaybeAutoTitle(c4.ID, "fix the flaky build"); err != nil {
		t.Fatal(err)
	}
	if got := title(c4.ID); got != "fix the flaky build" {
		t.Errorf("the session default wasn't auto-titled: %q", got)
	}
}

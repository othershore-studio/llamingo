// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/othershore-studio/llamingo/internal/store"
)

func TestChatsCreateAndList(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if chats, _ := s.ListChats(); len(chats) != 0 {
		t.Fatalf("a new store should have no chats, got %d", len(chats))
	}

	first, err := s.NewChat("First")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("chat id is empty")
	}
	time.Sleep(2 * time.Millisecond) // distinct Created timestamps for ordering
	if _, err := s.NewChat("Second"); err != nil {
		t.Fatal(err)
	}

	chats, err := s.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 2 {
		t.Fatalf("want 2 chats, got %d", len(chats))
	}
	if chats[0].Title != "Second" {
		t.Errorf("want newest (Second) first, got %q", chats[0].Title)
	}
}

func TestRename(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Old title")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(c.ID, "New title"); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Title != "New title" {
		t.Fatalf("after rename, chats = %+v, want one titled %q", chats, "New title")
	}
	// Renaming an unknown conversation is an error, not a silent no-op.
	if err := s.Rename("nonexistent", "x"); err == nil {
		t.Error("renaming an unknown conversation should error")
	}
}

func TestDelete(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.NewChat("A")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.NewChat("B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].Title != "B" {
		t.Fatalf("after delete, chats = %+v, want only B", chats)
	}
	// Deleting an unknown conversation is an error.
	if err := s.Delete("nonexistent"); err == nil {
		t.Error("deleting an unknown conversation should error")
	}
}

func TestConfig(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// A missing config file is the zero Config, not an error.
	c, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "" || c.Token != "" {
		t.Fatalf("fresh config = %+v, want empty", c)
	}
	if err := s.SetConfig(store.Config{URL: "https://lab.example/llama", Token: "abc"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Config()
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != "https://lab.example/llama" || got.Token != "abc" {
		t.Fatalf("saved config = %+v", got)
	}
}

func TestProjectsAndSessions(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	p, err := s.OpenProject(`C:\Code\demo`, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	if p.Path != `C:\Code\demo` {
		t.Errorf("project path = %q", p.Path)
	}

	// Re-opening the same folder reuses the project (lookup by id), never forks.
	again, err := s.OpenProject(`C:\Code\demo`, "Demo again")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != p.ID {
		t.Errorf("re-open forked the project: %q vs %q", again.ID, p.ID)
	}
	if projs, _ := s.ListProjects(); len(projs) != 1 {
		t.Fatalf("want 1 project, got %d", len(projs))
	}

	if _, err := s.NewSession(p.ID, "Task A"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.ListSessions(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess) != 1 || sess[0].Title != "Task A" {
		t.Fatalf("sessions = %+v, want one titled Task A", sess)
	}
}

func TestSelfHealingListing(t *testing.T) {
	root := t.TempDir()
	s, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	kept, err := s.NewChat("Keep")
	if err != nil {
		t.Fatal(err)
	}

	// A directory with no valid meta.json is ignored, not an error.
	if err := os.MkdirAll(filepath.Join(root, "chat", "not-a-conversation"), 0o755); err != nil {
		t.Fatal(err)
	}
	chats, err := s.ListChats()
	if err != nil {
		t.Fatal(err)
	}
	if len(chats) != 1 || chats[0].ID != kept.ID {
		t.Fatalf("junk directory was not skipped: %+v", chats)
	}

	// Deleting a conversation on disk just drops it from the listing — the
	// scan carries no stale state.
	if err := os.RemoveAll(filepath.Join(root, "chat", kept.ID)); err != nil {
		t.Fatal(err)
	}
	if chats, _ := s.ListChats(); len(chats) != 0 {
		t.Fatalf("deleted chat is still listed: %+v", chats)
	}
}

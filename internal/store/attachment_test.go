// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAttachment(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.NewChat("Pics")
	if err != nil {
		t.Fatal(err)
	}

	ref, err := s.SaveAttachment(c.ID, "ab12cd.png", []byte("PNGDATA"))
	if err != nil {
		t.Fatal(err)
	}
	if ref != "attachments/ab12cd.png" {
		t.Fatalf("ref = %q, want attachments/ab12cd.png", ref)
	}

	// The bytes land under the conversation's attachments/ folder.
	p, err := s.AttachmentPath(c.ID, "ab12cd.png")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(p)) != "attachments" {
		t.Errorf("attachment not under attachments/: %s", p)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PNGDATA" {
		t.Fatalf("saved bytes = %q, want PNGDATA", got)
	}

	// A missing attachment errors rather than returning a bogus path.
	if _, err := s.AttachmentPath(c.ID, "missing.png"); err == nil {
		t.Error("AttachmentPath for a missing file should error")
	}

	// Names that could escape the folder are rejected on save and on lookup.
	for _, bad := range []string{"../escape.png", `..\escape.png`, "a/b.png", "", ".."} {
		if _, err := s.SaveAttachment(c.ID, bad, []byte("x")); err == nil {
			t.Errorf("SaveAttachment(%q) should be rejected", bad)
		}
		if _, err := s.AttachmentPath(c.ID, bad); err == nil {
			t.Errorf("AttachmentPath(%q) should be rejected", bad)
		}
	}
}

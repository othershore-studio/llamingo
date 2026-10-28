// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"os"
	"testing"
)

func TestMemoryReadWrite(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c, tampered, err := s.Memory(); err != nil || c != "" || tampered {
		t.Fatalf("empty memory = %q tampered=%v err=%v", c, tampered, err)
	}
	if err := s.WriteMemory("remember: prefers tea"); err != nil {
		t.Fatal(err)
	}
	c, tampered, err := s.Memory()
	if err != nil {
		t.Fatal(err)
	}
	if c != "remember: prefers tea" || tampered {
		t.Fatalf("after write = %q tampered=%v, want the content and not tampered", c, tampered)
	}
}

func TestMemoryTamperDetection(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteMemory("original"); err != nil {
		t.Fatal(err)
	}

	// Simulate an edit made outside the app.
	if err := os.WriteFile(s.memoryFile(), []byte("changed outside"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, tampered, err := s.Memory()
	if err != nil {
		t.Fatal(err)
	}
	if c != "changed outside" {
		t.Errorf("content = %q, want the outside edit", c)
	}
	if !tampered {
		t.Error("expected tampered=true after an outside edit")
	}

	// Acknowledging the change clears the flag.
	if err := s.AckMemory(); err != nil {
		t.Fatal(err)
	}
	if _, tampered, _ := s.Memory(); tampered {
		t.Error("expected tampered=false after ack")
	}
}

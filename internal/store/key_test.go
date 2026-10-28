// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store_test

import (
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/store"
)

func TestPathHashCaseFolding(t *testing.T) {
	// Case-sensitive filesystem: case variants are distinct projects.
	if store.PathHash(`/home/d/code`, true) == store.PathHash(`/home/d/Code`, true) {
		t.Error("case-sensitive FS: case variants should differ")
	}
	// Case-insensitive filesystem: case variants share an identity.
	if store.PathHash(`/home/d/code`, false) != store.PathHash(`/home/d/Code`, false) {
		t.Error("case-insensitive FS: case variants should match")
	}
}

func TestPathHashStableAndSeparatorNormalized(t *testing.T) {
	a := store.PathHash(`C:\Code\llamingo`, false)
	b := store.PathHash(`C:/Code/llamingo`, false)
	if a != b {
		t.Errorf("separator variants gave different ids: %q vs %q", a, b)
	}
	if store.PathHash(`C:\Code\llamingo`, false) != a {
		t.Error("id is not stable across calls")
	}
}

func TestPathHashDistinct(t *testing.T) {
	if store.PathHash(`/a/one`, true) == store.PathHash(`/a/two`, true) {
		t.Error("distinct paths collided")
	}
}

func TestDirNameReadableAndHashed(t *testing.T) {
	// Separator variants share a directory name (slug + id both normalize).
	if store.DirName(`C:\Code\llamingo`, false) != store.DirName(`C:/Code/llamingo`, false) {
		t.Error("separator variants gave different dir names")
	}

	d := store.DirName(`C:\Code\llaminGO`, false)
	if !strings.HasPrefix(d, "C-Code-llaminGO-") {
		t.Errorf("dir name = %q, want a readable, case-preserving slug prefix", d)
	}
	id := d[strings.LastIndex(d, "-")+1:]
	if len(id) != 8 {
		t.Errorf("id segment = %q, want 8 hex chars", id)
	}
	for _, r := range id {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Errorf("id segment %q is not lowercase hex", id)
			break
		}
	}
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita

import (
	"errors"
	"path/filepath"
	"testing"
)

// accountsImpls returns each Accounts implementation to run the shared suite
// against — the Backend/lcpptest discipline: the in-memory fake and the real
// bbolt store must behave identically.
func accountsImpls(t *testing.T) map[string]Accounts {
	t.Helper()
	bolt, err := OpenAccounts(filepath.Join(t.TempDir(), "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { bolt.Close() })
	return map[string]Accounts{"mem": NewMemAccounts(), "bolt": bolt}
}

func TestAccountsCRUD(t *testing.T) {
	for name, acc := range accountsImpls(t) {
		t.Run(name, func(t *testing.T) {
			// Unknown name -> ErrNotFound.
			if _, err := acc.Get("nobody"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("Get unknown = %v, want ErrNotFound", err)
			}

			// Put + case-insensitive Get; password set and verified.
			dad := &Account{Name: "Dad", Role: RoleAdmin}
			if err := dad.SetPassword("hunter2"); err != nil {
				t.Fatal(err)
			}
			if err := acc.Put(dad); err != nil {
				t.Fatal(err)
			}
			got, err := acc.Get("dad") // lowercase lookup finds "Dad"
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != "Dad" || got.Role != RoleAdmin {
				t.Fatalf("Get = %+v", got)
			}
			if !got.HasPassword() || !got.CheckPassword("hunter2") || got.CheckPassword("wrong") {
				t.Fatal("password check failed")
			}

			// A passwordless profile accepts any attempt (presence = identity).
			kid := &Account{Name: "kid", Role: RoleUser}
			if err := acc.Put(kid); err != nil {
				t.Fatal(err)
			}
			k, err := acc.Get("kid")
			if err != nil {
				t.Fatal(err)
			}
			if k.HasPassword() || !k.CheckPassword("anything") {
				t.Fatal("passwordless profile should accept any password")
			}

			// List is name-ordered.
			list, err := acc.List()
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 2 || normName(list[0].Name) != "dad" || list[1].Name != "kid" {
				t.Fatalf("List = %+v", list)
			}

			// Delete.
			if err := acc.Delete("dad"); err != nil {
				t.Fatal(err)
			}
			if _, err := acc.Get("dad"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("after Delete, Get = %v, want ErrNotFound", err)
			}
		})
	}
}

// TestAccountsPersist proves the bbolt registry survives a Close/reopen —
// accounts are durable state, unlike the derived file-walk caches.
func TestAccountsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.db")
	acc, err := OpenAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	mom := &Account{Name: "mom", Role: RoleUser}
	if err := mom.SetPassword("s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := acc.Put(mom); err != nil {
		t.Fatal(err)
	}
	if err := acc.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenAccounts(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.Get("mom")
	if err != nil {
		t.Fatal(err)
	}
	if !got.CheckPassword("s3cret") {
		t.Fatal("password did not survive reopen")
	}
}

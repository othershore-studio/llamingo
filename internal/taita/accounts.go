// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package taita

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.etcd.io/bbolt"
	"golang.org/x/crypto/bcrypt"
)

// Role is an account's authority level. Only an admin may edit the server's own
// config (connections / Forge / MCP); regular users and anonymous sessions just
// chat. See docs/taita.md §6.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// Account is one named profile on a taita server. The password is OPTIONAL: an
// empty PassHash is a passwordless presence profile (presence = identity on a
// trusted mesh); a set PassHash protects the profile, and the admin always has
// one. Chats, memory and images are NOT stored here — they live in the account's
// file store. The account registry is the only table-shaped state, so it is the
// only thing in the accounts DB (docs/taita.md §6).
type Account struct {
	Name     string    `json:"name"`               // unique; lookup is case-insensitive, also the store-folder key
	Role     Role      `json:"role"`               // RoleUser | RoleAdmin
	PassHash []byte    `json:"passHash,omitempty"` // bcrypt; empty = passwordless
	Created  time.Time `json:"created,omitempty"`
}

// HasPassword reports whether the account is password-protected (vs a passwordless
// presence profile).
func (a *Account) HasPassword() bool { return len(a.PassHash) > 0 }

// SetPassword hashes and stores pw with bcrypt. An empty pw clears the password,
// returning the profile to passwordless.
func (a *Account) SetPassword(pw string) error {
	if pw == "" {
		a.PassHash = nil
		return nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	a.PassHash = h
	return nil
}

// CheckPassword reports whether pw matches. A passwordless account (no hash)
// accepts any attempt — presence is identity; whether that is allowed is the
// caller's auth-posture decision, not the store's.
func (a *Account) CheckPassword(pw string) bool {
	if !a.HasPassword() {
		return true
	}
	return bcrypt.CompareHashAndPassword(a.PassHash, []byte(pw)) == nil
}

// ErrNotFound is returned by Accounts.Get for an unknown name.
var ErrNotFound = errors.New("taita: account not found")

// Accounts is the account-registry seam — the same discipline as the Backend
// interface: the production impl is bbolt-backed (a DB, not SQL), tests use the
// in-memory fake, and the "repo vanishes" dependency risk collapses to a small
// JSON impl behind this interface. It stores ACCOUNTS ONLY. Implementations are
// safe for concurrent use.
type Accounts interface {
	// Get returns the account by name (case-insensitive), or ErrNotFound.
	Get(name string) (*Account, error)
	// Put creates or replaces an account.
	Put(a *Account) error
	// List returns every account, ordered by name.
	List() ([]*Account, error)
	// Delete removes an account; deleting an unknown name is not an error.
	Delete(name string) error
	// Close releases the backing store.
	Close() error
}

// normName is the canonical key form of an account name (the folder key and the
// bbolt key): case-insensitive, trimmed.
func normName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// --- bbolt-backed (production) ---------------------------------------------

var accountsBucket = []byte("accounts")

// boltAccounts is the production registry: one bbolt file, one bucket of
// normName(Name) -> JSON(Account). bbolt is pure-Go and cgo-free (repo policy).
type boltAccounts struct{ db *bbolt.DB }

// OpenAccounts opens (creating if needed) the bbolt accounts DB at path.
func OpenAccounts(path string) (Accounts, error) {
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("taita: open accounts db: %w", err)
	}
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(accountsBucket)
		return e
	}); err != nil {
		db.Close()
		return nil, fmt.Errorf("taita: init accounts bucket: %w", err)
	}
	return &boltAccounts{db: db}, nil
}

func (b *boltAccounts) Get(name string) (*Account, error) {
	var a *Account
	err := b.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(accountsBucket).Get([]byte(normName(name)))
		if v == nil {
			return ErrNotFound
		}
		a = &Account{}
		return json.Unmarshal(v, a)
	})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (b *boltAccounts) Put(a *Account) error {
	key := normName(a.Name)
	if key == "" {
		return errors.New("taita: account name is empty")
	}
	enc, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(accountsBucket).Put([]byte(key), enc)
	})
}

func (b *boltAccounts) List() ([]*Account, error) {
	var out []*Account
	err := b.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(accountsBucket).ForEach(func(_, v []byte) error {
			a := &Account{}
			if e := json.Unmarshal(v, a); e != nil {
				return e
			}
			out = append(out, a)
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return normName(out[i].Name) < normName(out[j].Name) })
	return out, nil
}

func (b *boltAccounts) Delete(name string) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(accountsBucket).Delete([]byte(normName(name)))
	})
}

func (b *boltAccounts) Close() error { return b.db.Close() }

// --- in-memory (test fake) -------------------------------------------------

// memAccounts is an in-memory Accounts for tests — the lcpptest analogue of the
// bbolt registry. It copies accounts in and out so callers can't mutate stored
// state through a shared pointer.
type memAccounts struct {
	mu sync.Mutex
	m  map[string]*Account
}

// NewMemAccounts returns an empty in-memory registry.
func NewMemAccounts() Accounts { return &memAccounts{m: map[string]*Account{}} }

func (x *memAccounts) Get(name string) (*Account, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	a, ok := x.m[normName(name)]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *a
	return &cp, nil
}

func (x *memAccounts) Put(a *Account) error {
	key := normName(a.Name)
	if key == "" {
		return errors.New("taita: account name is empty")
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	cp := *a
	x.m[key] = &cp
	return nil
}

func (x *memAccounts) List() ([]*Account, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	out := make([]*Account, 0, len(x.m))
	for _, a := range x.m {
		cp := *a
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return normName(out[i].Name) < normName(out[j].Name) })
	return out, nil
}

func (x *memAccounts) Delete(name string) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.m, normName(name))
	return nil
}

func (x *memAccounts) Close() error { return nil }

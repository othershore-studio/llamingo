// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package permission

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// GrantStore persists "always allow" (persistent) grants by tool id.
type GrantStore interface {
	Granted(toolID string) (bool, error)
	Grant(toolID string) error
	Revoke(toolID string) error
}

// MemoryGrantStore is an in-memory GrantStore for tests or an ephemeral gate.
type MemoryGrantStore struct {
	mu sync.Mutex
	m  map[string]bool
}

// NewMemoryGrantStore returns an empty in-memory grant store.
func NewMemoryGrantStore() *MemoryGrantStore { return &MemoryGrantStore{m: map[string]bool{}} }

func (s *MemoryGrantStore) Granted(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[id], nil
}

func (s *MemoryGrantStore) Grant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[id] = true
	return nil
}

func (s *MemoryGrantStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, id)
	return nil
}

// FileGrantStore persists grants as a plain JSON array of tool ids — a file the
// user owns and can read or edit. It reads the file on each operation and
// rewrites it on every change, so it is always consistent with what is on disk.
type FileGrantStore struct {
	path string
	mu   sync.Mutex
}

// NewFileGrantStore returns a grant store backed by the file at path.
func NewFileGrantStore(path string) *FileGrantStore { return &FileGrantStore{path: path} }

func (s *FileGrantStore) Granted(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.load()
	if err != nil {
		return false, err
	}
	_, ok := ids[id]
	return ok, nil
}

func (s *FileGrantStore) Grant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.load()
	if err != nil {
		return err
	}
	ids[id] = struct{}{}
	return s.save(ids)
}

func (s *FileGrantStore) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.load()
	if err != nil {
		return err
	}
	delete(ids, id)
	return s.save(ids)
}

func (s *FileGrantStore) load() (map[string]struct{}, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]struct{}{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	m := make(map[string]struct{}, len(list))
	for _, id := range list {
		m[id] = struct{}{}
	}
	return m, nil
}

func (s *FileGrantStore) save(ids map[string]struct{}) error {
	list := make([]string, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	sort.Strings(list)
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o644)
}

var (
	_ GrantStore = (*MemoryGrantStore)(nil)
	_ GrantStore = (*FileGrantStore)(nil)
)

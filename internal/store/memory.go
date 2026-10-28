// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func (s *Store) memoryFile() string     { return filepath.Join(s.root, "memory.md") }
func (s *Store) memoryHashFile() string { return filepath.Join(s.root, ".memory.hash") }

// Memory returns the personal memory and whether it was edited outside the app
// since it was last written or acknowledged (its content hash no longer matches
// the stored one). A missing file is empty memory, not tampering. The hash is a
// plain SHA-256 for now; a keyed digest (seed/HMAC) is a later hardening — see
// docs/architecture.md → "Memory integrity".
func (s *Store) Memory() (content string, tampered bool, err error) {
	b, err := os.ReadFile(s.memoryFile())
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: read memory: %w", err)
	}
	content = string(b)
	stored, herr := os.ReadFile(s.memoryHashFile())
	if herr != nil {
		return content, false, nil // no stored hash yet — nothing to compare against
	}
	tampered = strings.TrimSpace(string(stored)) != hashContent(content)
	return content, tampered, nil
}

// WriteMemory replaces the personal memory and records its hash, so a later edit
// from outside the app is detectable.
func (s *Store) WriteMemory(content string) error {
	if err := os.WriteFile(s.memoryFile(), []byte(content), 0o644); err != nil {
		return fmt.Errorf("store: write memory: %w", err)
	}
	return s.storeMemoryHash(content)
}

// AckMemory records the current memory's hash, clearing a tamper flag once the
// user has seen (and accepted) an edit made outside the app.
func (s *Store) AckMemory() error {
	b, err := os.ReadFile(s.memoryFile())
	if errors.Is(err, fs.ErrNotExist) {
		return s.storeMemoryHash("")
	}
	if err != nil {
		return fmt.Errorf("store: read memory: %w", err)
	}
	return s.storeMemoryHash(string(b))
}

func (s *Store) storeMemoryHash(content string) error {
	if err := os.WriteFile(s.memoryHashFile(), []byte(hashContent(content)), 0o644); err != nil {
		return fmt.Errorf("store: write memory hash: %w", err)
	}
	return nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

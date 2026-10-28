// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package store is the plain-file store: a folder of conversations you own,
// with a rebuildable index on top. See docs/architecture.md → Storage.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"runtime"
	"strings"
)

// ProjectID is the stable identity of a folder-anchored project: a short hash
// of the canonical absolute path. Projects are looked up by this, so the
// cosmetic directory slug may differ (by case, say) without forking a project.
type ProjectID string

// PathHash returns the ProjectID for an absolute working-folder path.
// caseSensitive reports whether the filesystem hosting the path treats case as
// significant (use DetectCaseSensitive when unsure). It is the one place case
// enters: "/a/Code" and "/a/code" are two projects on a case-sensitive
// filesystem and one on a case-insensitive one — both correct.
func PathHash(absPath string, caseSensitive bool) ProjectID {
	sum := sha256.Sum256([]byte(canonicalPath(absPath, caseSensitive)))
	return ProjectID(hex.EncodeToString(sum[:])[:8])
}

// DirName is the on-disk directory name for a project: a readable,
// case-preserving slug plus the ProjectID. Identity is the ID suffix; the slug
// is decoration — so two case-variant paths on a case-insensitive filesystem
// share an ID, and the store reuses the existing directory rather than forking.
func DirName(absPath string, caseSensitive bool) string {
	return slug(absPath) + "-" + string(PathHash(absPath, caseSensitive))
}

// canonicalPath reduces a path to the identity string that gets hashed:
// lexically cleaned, separators normalized to '/', and case-folded only when
// the filesystem is case-insensitive.
//
// TODO(nfc): also NFC-normalize once golang.org/x/text is available — macOS
// stores NFD and Linux NFC, so an accented path can otherwise fork. Deferred to
// keep the store stdlib-only for now.
func canonicalPath(absPath string, caseSensitive bool) string {
	p := strings.ReplaceAll(filepath.Clean(absPath), `\`, "/")
	if !caseSensitive {
		p = strings.ToLower(p)
	}
	return p
}

// slug is a cosmetic, case-preserving rendering of the path for humans reading
// the store directory. It never decides identity (the hash does), so lossy
// sanitization and the odd collision are harmless.
func slug(absPath string) string {
	p := strings.ReplaceAll(filepath.Clean(absPath), `\`, "/")
	var b strings.Builder
	dash := false
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "project"
	}
	return s
}

// DetectCaseSensitive reports whether the filesystem at path is case-sensitive.
// For now it is a GOOS heuristic (Windows and macOS default to insensitive,
// others to sensitive).
//
// TODO(probe): stat a known path under both casings on the hosting volume
// instead of trusting GOOS — macOS can be case-sensitive if formatted so, and
// WSL/Linux mounts vary.
func DetectCaseSensitive(path string) bool {
	switch runtime.GOOS {
	case "windows", "darwin":
		return false
	default:
		return true
	}
}

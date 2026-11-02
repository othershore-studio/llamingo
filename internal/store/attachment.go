// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ListAttachments returns the base names of the files in a conversation's attachments/
// folder (none → empty, not an error). Each name resolves through AttachmentPath. Used to
// recover a resumed conversation's images for the terminal viewer.
func (s *Store) ListAttachments(convID string) ([]string, error) {
	dir, err := s.conversationDir(convID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(dir, "attachments"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: list attachments: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// SaveAttachment writes bytes into a conversation's attachments/ folder under a
// plain base name and returns the reference stored in the log: "attachments/<name>".
// That ref resolves next to conversation.md on disk (so the file stays portable)
// and the web UI maps it to the serve route. It is the store's shared attachment
// primitive — image generation (imagegen.AttachmentSink) and persisting images
// that tools return both go through it, so there is one attachments layout.
func (s *Store) SaveAttachment(convID, name string, data []byte) (string, error) {
	if err := safeAttachmentName(name); err != nil {
		return "", err
	}
	dir, err := s.conversationDir(convID)
	if err != nil {
		return "", err
	}
	attDir := filepath.Join(dir, "attachments")
	if err := os.MkdirAll(attDir, 0o755); err != nil {
		return "", fmt.Errorf("store: attachments dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(attDir, name), data, 0o644); err != nil {
		return "", fmt.Errorf("store: write attachment: %w", err)
	}
	return "attachments/" + name, nil
}

// AttachmentPath resolves a conversation attachment to an absolute path for
// serving, rejecting any name that is not a plain base name and any file that
// does not exist.
func (s *Store) AttachmentPath(convID, name string) (string, error) {
	if err := safeAttachmentName(name); err != nil {
		return "", err
	}
	dir, err := s.conversationDir(convID)
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, "attachments", name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("store: attachment %q: %w", name, err)
	}
	return p, nil
}

// LogDir returns (creating it) the logs/ folder inside a conversation's own
// directory, where debug-mode payloads are written. It sits beside attachments/
// in the conversation folder, so deleting the conversation clears its logs too.
func (s *Store) LogDir(convID string) (string, error) {
	dir, err := s.conversationDir(convID)
	if err != nil {
		return "", err
	}
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return "", fmt.Errorf("store: logs dir: %w", err)
	}
	return logDir, nil
}

// safeAttachmentName rejects anything that is not a single path element — no
// separators, no "..", not empty — so a name taken from a URL or the model can
// never escape the attachments/ folder.
func safeAttachmentName(name string) error {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("store: unsafe attachment name %q", name)
	}
	return nil
}

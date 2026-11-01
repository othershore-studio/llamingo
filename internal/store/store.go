// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Store is the plain-file store rooted at a directory (e.g. ~/.llamingo). Two
// roots live under it: chat/ (free chats) and folder/ (projects, each holding
// many sessions). Canonical state is the files; any index is derived by
// scanning them, so there is no stale state to lose — see docs/architecture.md.
type Store struct {
	root          string
	caseSensitive bool
}

// Open prepares a store rooted at root, creating it if needed.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("store: create root: %w", err)
	}
	return &Store{root: root, caseSensitive: DetectCaseSensitive(root)}, nil
}

// ConversationMeta is the per-conversation metadata persisted as meta.json next
// to conversation.md.
type ConversationMeta struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	Model         string    `json:"model,omitempty"`
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`
	DisabledTools []string  `json:"disabledTools,omitempty"` // tools curated off for this chat (per-chat; see registryFor)

	// LastBudget is the context accounting from this chat's most recent turn,
	// shown in the status bar on reopen without re-metering. Nil until a turn runs.
	LastBudget *BudgetStatus `json:"lastBudget,omitempty"`
}

// ProjectMeta is the per-project metadata persisted as folder/<dir>/meta.json.
// Path is the exact absolute working-folder path; ID is derived from it.
type ProjectMeta struct {
	ID      ProjectID `json:"id"`
	Path    string    `json:"path"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
}

func (s *Store) folderRoot() string { return filepath.Join(s.root, "folder") }

// NewChat creates a free chat and returns its metadata.
func (s *Store) NewChat(title string) (ConversationMeta, error) {
	id, err := newID()
	if err != nil {
		return ConversationMeta{}, err
	}
	now := time.Now().UTC()
	m := ConversationMeta{ID: id, Title: title, Created: now, Updated: now}
	if err := s.scaffoldConversation(filepath.Join(s.root, "chat", id), m); err != nil {
		return ConversationMeta{}, err
	}
	return m, nil
}

// ListChats returns the free chats, newest first.
func (s *Store) ListChats() ([]ConversationMeta, error) {
	return s.listConversations(filepath.Join(s.root, "chat"))
}

// OpenProject returns the project for absPath, creating it on first use. Lookup
// is by ProjectID, so re-opening the same folder reuses the existing project
// rather than forking one.
func (s *Store) OpenProject(absPath, title string) (ProjectMeta, error) {
	id := PathHash(absPath, s.caseSensitive)
	existing, ok, err := s.findProject(id)
	if err != nil {
		return ProjectMeta{}, err
	}
	if ok {
		return existing, nil
	}
	dir := filepath.Join(s.folderRoot(), DirName(absPath, s.caseSensitive))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ProjectMeta{}, fmt.Errorf("store: create project dir: %w", err)
	}
	m := ProjectMeta{ID: id, Path: absPath, Title: title, Created: time.Now().UTC()}
	if err := writeJSON(filepath.Join(dir, "meta.json"), m); err != nil {
		return ProjectMeta{}, err
	}
	return m, nil
}

// ListProjects returns the known projects, newest first. Directories without a
// valid meta.json are skipped.
func (s *Store) ListProjects() ([]ProjectMeta, error) {
	entries, err := os.ReadDir(s.folderRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	var out []ProjectMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var m ProjectMeta
		if readJSON(filepath.Join(s.folderRoot(), e.Name(), "meta.json"), &m) != nil {
			continue // self-heal: skip anything that is not a project
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// NewSession creates a session inside a project and returns its metadata.
func (s *Store) NewSession(projectID ProjectID, title string) (ConversationMeta, error) {
	p, ok, err := s.findProject(projectID)
	if err != nil {
		return ConversationMeta{}, err
	}
	if !ok {
		return ConversationMeta{}, fmt.Errorf("store: unknown project %q", projectID)
	}
	id, err := newID()
	if err != nil {
		return ConversationMeta{}, err
	}
	now := time.Now().UTC()
	// A new session inherits the project's tool defaults (it can still override
	// them per chat); see .llamingo/project.json.
	defaults, _ := s.ProjectToolDefaults(p.Path)
	m := ConversationMeta{ID: id, Title: title, Created: now, Updated: now, DisabledTools: defaults}
	dir := filepath.Join(s.folderRoot(), DirName(p.Path, s.caseSensitive), id)
	if err := s.scaffoldConversation(dir, m); err != nil {
		return ConversationMeta{}, err
	}
	return m, nil
}

// ListSessions returns a project's sessions, newest first.
func (s *Store) ListSessions(projectID ProjectID) ([]ConversationMeta, error) {
	p, ok, err := s.findProject(projectID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("store: unknown project %q", projectID)
	}
	return s.listConversations(filepath.Join(s.folderRoot(), DirName(p.Path, s.caseSensitive)))
}

func (s *Store) findProject(id ProjectID) (ProjectMeta, bool, error) {
	ps, err := s.ListProjects()
	if err != nil {
		return ProjectMeta{}, false, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, true, nil
		}
	}
	return ProjectMeta{}, false, nil
}

// ProjectRoot returns the absolute working folder of the project a conversation
// belongs to. A free chat (under chat/) has none, so ok is false; an unknown
// conversation is likewise reported as "no project" rather than an error. This
// is the folder the project file tools are confined to.
func (s *Store) ProjectRoot(convID string) (string, bool, error) {
	if fileExists(filepath.Join(s.root, "chat", convID, "meta.json")) {
		return "", false, nil // a free chat has no project folder
	}
	entries, err := os.ReadDir(s.folderRoot())
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: find project for %q: %w", convID, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		projDir := filepath.Join(s.folderRoot(), e.Name())
		if !fileExists(filepath.Join(projDir, convID, "meta.json")) {
			continue
		}
		var m ProjectMeta
		if err := readJSON(filepath.Join(projDir, "meta.json"), &m); err != nil {
			return "", false, fmt.Errorf("store: read project meta for %q: %w", convID, err)
		}
		return m.Path, true, nil
	}
	return "", false, nil
}

// scaffoldConversation lays down a conversation directory: meta.json, a titled
// conversation.md placeholder (full message/compaction serialization comes in a
// later increment), and an attachments/ folder.
func (s *Store) scaffoldConversation(dir string, m ConversationMeta) error {
	if err := os.MkdirAll(filepath.Join(dir, "attachments"), 0o755); err != nil {
		return fmt.Errorf("store: create conversation dir: %w", err)
	}
	if err := writeJSON(filepath.Join(dir, "meta.json"), m); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation.md"), []byte("# "+m.Title+"\n"), 0o644); err != nil {
		return fmt.Errorf("store: write conversation.md: %w", err)
	}
	return nil
}

// listConversations scans parent for conversation directories (those holding a
// valid meta.json), newest first. Anything else — attachments/, a half-written
// directory, foreign files — is skipped, so the listing self-heals.
func (s *Store) listConversations(parent string) ([]ConversationMeta, error) {
	entries, err := os.ReadDir(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: list conversations: %w", err)
	}
	var out []ConversationMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		var m ConversationMeta
		if readJSON(filepath.Join(parent, e.Name(), "meta.json"), &m) != nil || m.ID == "" {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("store: write %s: %w", filepath.Base(path), err)
	}
	return nil
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

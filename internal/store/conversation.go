// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EntryKind discriminates the typed entries in a conversation log.
type EntryKind string

const (
	KindMessage   EntryKind = "msg"
	KindSummary   EntryKind = "summary"
	KindReasoning EntryKind = "reasoning" // the model's thinking trace for the answer that follows it
)

// Entry is one entry in a conversation's append-only log (conversation.md). A
// message carries a role and body; a summary (compaction — a later increment)
// shadows an id-range rather than being deleted. The log is only appended to,
// never rewritten, so the file on disk stays the full truth — see
// docs/architecture.md → "Compaction is a projection, never a deletion".
type Entry struct {
	ID      string    `json:"id"`
	Kind    EntryKind `json:"kind"`
	Role    string    `json:"role,omitempty"` // msg: user|assistant|system|tool
	Time    time.Time `json:"time"`
	Content string    `json:"content"`

	// Summary-only (Kind == KindSummary): the id-range this summary shadows and
	// the accounting the context builder reports when it substitutes it.
	CoversFrom    string `json:"coversFrom,omitempty"`
	CoversTo      string `json:"coversTo,omitempty"`
	Model         string `json:"model,omitempty"`
	Replaced      int    `json:"replaced,omitempty"`
	SummaryTokens int    `json:"summaryTokens,omitempty"`
}

// AppendMessage appends a message entry to a conversation's log and bumps its
// Updated time. Append-only: nothing already written is touched.
func (s *Store) AppendMessage(id, role, content string) (Entry, error) {
	dir, err := s.conversationDir(id)
	if err != nil {
		return Entry{}, err
	}
	eid, err := newEntryID()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{ID: eid, Kind: KindMessage, Role: role, Time: time.Now().UTC(), Content: content}
	if err := appendEntry(filepath.Join(dir, "conversation.md"), e); err != nil {
		return Entry{}, err
	}
	if err := touchUpdated(filepath.Join(dir, "meta.json"), e.Time); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// AppendAssistant appends an assistant turn: its reasoning trace (when non-empty,
// as a reasoning entry immediately before the answer) and then the answer itself.
// A reasoning entry is display-only — Project keeps only messages and summaries,
// so the trace is shown when a chat is reopened but never re-enters the prompt or
// the token budget. See docs/architecture.md → "Effort / thinking".
func (s *Store) AppendAssistant(id, content, reasoning string) (Entry, error) {
	dir, err := s.conversationDir(id)
	if err != nil {
		return Entry{}, err
	}
	logPath := filepath.Join(dir, "conversation.md")
	now := time.Now().UTC()
	if strings.TrimSpace(reasoning) != "" {
		rid, err := newEntryID()
		if err != nil {
			return Entry{}, err
		}
		if err := appendEntry(logPath, Entry{ID: rid, Kind: KindReasoning, Time: now, Content: reasoning}); err != nil {
			return Entry{}, err
		}
	}
	eid, err := newEntryID()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{ID: eid, Kind: KindMessage, Role: "assistant", Time: now, Content: content}
	if err := appendEntry(logPath, e); err != nil {
		return Entry{}, err
	}
	if err := touchUpdated(filepath.Join(dir, "meta.json"), now); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// LoadConversation returns a conversation's entries in log order.
func (s *Store) LoadConversation(id string) ([]Entry, error) {
	dir, err := s.conversationDir(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "conversation.md"))
	if err != nil {
		return nil, fmt.Errorf("store: read conversation log: %w", err)
	}
	_, entries := parseConversation(data)
	return entries, nil
}

// SummaryInput is the data for a compaction summary entry: the id-range it
// shadows, the model that produced it, the summary text, and the token
// accounting (what it replaced vs. its own size) for honest reporting.
type SummaryInput struct {
	CoversFrom    string
	CoversTo      string
	Model         string
	Content       string
	Replaced      int
	SummaryTokens int
}

// AppendSummary appends a compaction summary entry shadowing [CoversFrom,
// CoversTo]. Append-only: the messages it summarizes are left untouched, so the
// file on disk stays the full truth.
func (s *Store) AppendSummary(id string, in SummaryInput) (Entry, error) {
	dir, err := s.conversationDir(id)
	if err != nil {
		return Entry{}, err
	}
	eid, err := newEntryID()
	if err != nil {
		return Entry{}, err
	}
	e := Entry{
		ID: eid, Kind: KindSummary, Time: time.Now().UTC(),
		CoversFrom: in.CoversFrom, CoversTo: in.CoversTo,
		Model: in.Model, Content: in.Content,
		Replaced: in.Replaced, SummaryTokens: in.SummaryTokens,
	}
	if err := appendEntry(filepath.Join(dir, "conversation.md"), e); err != nil {
		return Entry{}, err
	}
	if err := touchUpdated(filepath.Join(dir, "meta.json"), e.Time); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// Project applies compaction to a conversation's entries: the active summary
// (the one shadowing the largest prefix of messages) stands in for the messages
// it covers, followed by the messages it does not. Without a summary the
// messages are returned unchanged. The stored log is never modified — this is
// the projection the context builder sends to the model. Entries that are
// neither messages nor summaries (reasoning traces) are display-only and
// excluded here, so the thinking never re-enters the prompt.
func Project(entries []Entry) []Entry {
	var msgs, summaries []Entry
	for _, e := range entries {
		switch e.Kind {
		case KindMessage:
			msgs = append(msgs, e)
		case KindSummary:
			summaries = append(summaries, e)
		}
	}
	if len(summaries) == 0 {
		return msgs
	}
	active, cut := activeSummary(msgs, summaries)
	if active == nil {
		return msgs
	}
	return append([]Entry{*active}, msgs[cut+1:]...)
}

// ActiveSummary returns the summary shadowing the largest prefix of a
// conversation's messages and the number of messages it covers (nil, 0 when
// there is no summary). Compaction uses it to skip already-folded messages.
func ActiveSummary(entries []Entry) (*Entry, int) {
	var msgs, summaries []Entry
	for _, e := range entries {
		switch e.Kind {
		case KindMessage:
			msgs = append(msgs, e)
		case KindSummary:
			summaries = append(summaries, e)
		}
	}
	if len(summaries) == 0 {
		return nil, 0
	}
	active, cut := activeSummary(msgs, summaries)
	if active == nil {
		return nil, 0
	}
	return active, cut + 1
}

// activeSummary returns the summary whose covered range reaches furthest along
// the message sequence, and the index of its last covered message (-1 if none
// of the summaries name a known message).
func activeSummary(msgs, summaries []Entry) (*Entry, int) {
	best := -1
	var active *Entry
	for i := range summaries {
		if c := indexOfID(msgs, summaries[i].CoversTo); c > best {
			best = c
			active = &summaries[i]
		}
	}
	return active, best
}

func indexOfID(msgs []Entry, id string) int {
	for i := range msgs {
		if msgs[i].ID == id {
			return i
		}
	}
	return -1
}

// conversationDir resolves a conversation id to its directory, whether it is a
// free chat (chat/<id>) or a project session (folder/<project>/<id>).
func (s *Store) conversationDir(id string) (string, error) {
	chatDir := filepath.Join(s.root, "chat", id)
	if fileExists(filepath.Join(chatDir, "meta.json")) {
		return chatDir, nil
	}
	if entries, err := os.ReadDir(s.folderRoot()); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			sessDir := filepath.Join(s.folderRoot(), e.Name(), id)
			if fileExists(filepath.Join(sessDir, "meta.json")) {
				return sessDir, nil
			}
		}
	}
	return "", fmt.Errorf("store: conversation %q not found", id)
}

func appendEntry(path string, e Entry) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("store: open conversation log: %w", err)
	}
	defer f.Close()
	block := "\n" + marshalMarker(e) + "\n\n" + strings.TrimRight(e.Content, "\n") + "\n"
	if _, err := f.WriteString(block); err != nil {
		return fmt.Errorf("store: append conversation log: %w", err)
	}
	return nil
}

func touchUpdated(metaPath string, t time.Time) error {
	var m ConversationMeta
	if err := readJSON(metaPath, &m); err != nil {
		return fmt.Errorf("store: read meta for update: %w", err)
	}
	m.Updated = t
	return writeJSON(metaPath, m)
}

// Rename changes a conversation's display title in meta.json. The append-only
// conversation.md is left untouched — its "# title" header is only a file header;
// the title the app lists and shows comes from meta.json.
func (s *Store) Rename(id, title string) error {
	dir, err := s.conversationDir(id)
	if err != nil {
		return err
	}
	metaPath := filepath.Join(dir, "meta.json")
	var m ConversationMeta
	if err := readJSON(metaPath, &m); err != nil {
		return fmt.Errorf("store: read meta for rename: %w", err)
	}
	m.Title = title
	return writeJSON(metaPath, m)
}

// Delete removes a conversation (chat or session) and its directory from disk.
// The listing self-heals from the files, so nothing else needs updating.
func (s *Store) Delete(id string) error {
	dir, err := s.conversationDir(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("store: delete conversation: %w", err)
	}
	return nil
}

// marshalEntries renders a full conversation.md: a human title line, then each
// entry as an HTML-comment marker (machine-readable metadata, invisible in any
// Markdown viewer) followed by its body. appendEntry writes the same per-entry
// shape, so an appended log and a marshaled one parse identically.
func marshalEntries(title string, entries []Entry) []byte {
	var b strings.Builder
	b.WriteString("# ")
	b.WriteString(title)
	b.WriteString("\n")
	for _, e := range entries {
		b.WriteString("\n")
		b.WriteString(marshalMarker(e))
		b.WriteString("\n\n")
		b.WriteString(strings.TrimRight(e.Content, "\n"))
		b.WriteString("\n")
	}
	return []byte(b.String())
}

// marshalMarker renders an entry's marker line: a visible bold speaker label
// (so a Markdown viewer shows who said what) then the machine-readable HTML
// comment (invisible when rendered). parseConversation reads the comment and
// ignores the label, which marshalMarker regenerates from the role on write.
func marshalMarker(e Entry) string {
	return entryLabel(e) + " " + rawMarker(e)
}

func rawMarker(e Entry) string {
	if e.Kind == KindSummary {
		return fmt.Sprintf("<!-- llamingo:summary id=%s covers=%s..%s model=%s t=%s replaced=%d summary=%d -->",
			e.ID, e.CoversFrom, e.CoversTo, e.Model, e.Time.UTC().Format(time.RFC3339), e.Replaced, e.SummaryTokens)
	}
	if e.Kind == KindReasoning {
		return fmt.Sprintf("<!-- llamingo:reasoning id=%s t=%s -->",
			e.ID, e.Time.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("<!-- llamingo:msg id=%s role=%s t=%s -->",
		e.ID, e.Role, e.Time.UTC().Format(time.RFC3339))
}

// entryLabel is the bold speaker label shown before an entry's marker.
func entryLabel(e Entry) string {
	switch e.Kind {
	case KindSummary:
		return "**Summary**"
	case KindReasoning:
		return "**Thinking**"
	}
	switch e.Role {
	case "user":
		return "**You**"
	case "assistant":
		return "**Assistant**"
	case "system":
		return "**System**"
	case "tool":
		return "**Tool**"
	}
	return "**" + e.Role + "**"
}

// A marker line may carry a visible speaker-label prefix (see marshalMarker), so
// the comment is matched at the end of the line rather than anchored to its start.
var entryMarker = regexp.MustCompile(`<!--\s*llamingo:(\w+)\s+(.*?)\s*-->\s*$`)

// parseConversation parses a conversation.md into its title and entries. It is
// lenient: a line that is not a recognized marker is body text for the current
// entry (or preamble before the first). A body line that itself looks like a
// marker would be misread — rare, and only reachable by hand-editing.
func parseConversation(data []byte) (string, []Entry) {
	var (
		title   string
		entries []Entry
		cur     *Entry
		content []string
	)
	flush := func() {
		if cur != nil {
			cur.Content = strings.Trim(strings.Join(content, "\n"), "\n")
			entries = append(entries, *cur)
		}
		cur, content = nil, nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := entryMarker.FindStringSubmatch(line); m != nil {
			flush()
			attrs := parseAttrs(m[2])
			e := Entry{Kind: EntryKind(m[1]), ID: attrs["id"], Time: parseTimeAttr(attrs["t"])}
			if e.Kind == KindSummary {
				e.CoversFrom, e.CoversTo = splitCovers(attrs["covers"])
				e.Model = attrs["model"]
				e.Replaced = atoi(attrs["replaced"])
				e.SummaryTokens = atoi(attrs["summary"])
			} else {
				e.Role = attrs["role"]
			}
			cur = &e
			continue
		}
		if cur == nil {
			if title == "" {
				if s := strings.TrimSpace(line); strings.HasPrefix(s, "# ") {
					title = strings.TrimSpace(s[2:])
				}
			}
			continue
		}
		content = append(content, line)
	}
	flush()
	return title, entries
}

func parseAttrs(s string) map[string]string {
	out := make(map[string]string)
	for _, f := range strings.Fields(s) {
		if i := strings.IndexByte(f, '='); i > 0 {
			out[f[:i]] = f[i+1:]
		}
	}
	return out
}

func parseTimeAttr(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func splitCovers(s string) (string, string) {
	if i := strings.Index(s, ".."); i >= 0 {
		return s[:i], s[i+2:]
	}
	return s, ""
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func newEntryID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("store: generate entry id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

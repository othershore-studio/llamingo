// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/imagegen"
	"github.com/othershore-studio/llamingo/internal/lcpptest"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
)

// allowPrompter approves any tool request. The floor test uses no tools, so it just
// satisfies the gate's prompter.
type allowPrompter struct{}

func (allowPrompter) Ask(context.Context, permission.Request, []permission.Scope) (permission.Decision, error) {
	return permission.Decision{Allow: true, Scope: permission.ScopeOnce}, nil
}

// A turn against the fake streams the reply, holds the exchange in memory, persists
// both sides to the store, and reports the authoritative budget — the floor working
// CLI-direct over the core, no HTTP.
func TestSessionTurnAgainstFake(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}

	var streamed strings.Builder
	if err := sess.Turn(context.Background(), "Hi", func(d backend.Delta) error {
		streamed.WriteString(d.Content)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(streamed.String()); got != "Hello! How can I help you today?" {
		t.Errorf("streamed reply = %q", got)
	}
	// Held in memory: user + assistant.
	if len(sess.msgs) != 2 || sess.msgs[0].Role != backend.RoleUser || sess.msgs[1].Role != backend.RoleAssistant {
		t.Fatalf("messages = %+v, want user + assistant", sess.msgs)
	}
	if !strings.Contains(sess.msgs[1].Content, "Hello") {
		t.Errorf("assistant message = %q", sess.msgs[1].Content)
	}
	// Persisted to the store (survives a reopen): just the two messages, no tool trail.
	entries, err := st.LoadConversation(sess.convID)
	if err != nil {
		t.Fatal(err)
	}
	var user, asst int
	for _, e := range entries {
		switch e.Role {
		case "user":
			user++
		case "assistant":
			asst++
		}
	}
	if user != 1 || asst != 1 {
		t.Fatalf("persisted messages: user=%d assistant=%d, want 1 each; entries=%+v", user, asst, entries)
	}
	// Via mcp.Run the model's reasoning is captured as a display-only trail entry.
	if len(entries) < 3 {
		t.Errorf("expected a reasoning trail entry too; got %d entries", len(entries))
	}
	// The authoritative budget comes back from the stream's usage.
	if b := sess.Budget(); b.Authoritative != 12 {
		t.Errorf("budget authoritative = %d, want 12", b.Authoritative)
	}
}

// The /tools view toggles a tool's curation per-chat: it persists to the store and the
// turn's registry is curated fresh from that set, so a disabled tool is no longer
// offered to the model. This drives the same Session accessors the view does, without
// the UI.
func TestSessionToolCuration(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}

	tools := sess.Tools()
	if len(tools) == 0 {
		t.Fatal("expected native tools in the base registry")
	}
	first := tools[0].Name

	// A fresh chat curates nothing off — the whole set is offered.
	if d := sess.disabledSet(); len(d) != 0 {
		t.Fatalf("fresh chat disabled set = %v, want empty", d)
	}

	// Toggle one off: now-disabled, persisted, and dropped from the curated registry a
	// turn would use.
	nowDisabled, err := sess.ToggleTool(first)
	if err != nil {
		t.Fatal(err)
	}
	if !nowDisabled {
		t.Fatalf("ToggleTool(%q) = false, want now-disabled", first)
	}
	if d := sess.disabledSet(); !d[first] {
		t.Errorf("disabled set %v missing %q after toggle", d, first)
	}
	curated := mcp.Curate(sess.reg, sess.disabledSet()).Tools()
	if len(curated) != len(tools)-1 {
		t.Errorf("curated tools = %d, want %d", len(curated), len(tools)-1)
	}
	for _, tl := range curated {
		if tl.Name == first {
			t.Errorf("curated registry still offers disabled tool %q", first)
		}
	}

	// Persisted to meta.json (survives a reopen).
	got, err := st.ConversationTools(sess.convID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != first {
		t.Errorf("persisted disabled = %v, want [%q]", got, first)
	}

	// Toggle back on: the set empties.
	nowDisabled, err = sess.ToggleTool(first)
	if err != nil {
		t.Fatal(err)
	}
	if nowDisabled {
		t.Errorf("second ToggleTool(%q) = true, want now-enabled", first)
	}
	if d := sess.disabledSet(); len(d) != 0 {
		t.Errorf("disabled set %v, want empty after toggling back", d)
	}
}

// A terminal turn gets the same system framing as the desktop: the working-directory
// note always (so the model knows its cwd), the personal memory once saved (so it can
// recall it — the write path alone never fed memory back), and a persona first when set.
func TestSessionSystemSegments(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}

	// Fresh project session: the working-directory note, nothing else.
	names := segNames(sess.systemSegments())
	if !contains(names, "workdir") {
		t.Errorf("system segments %v missing workdir", names)
	}
	if contains(names, "memory") || contains(names, "system") {
		t.Errorf("unexpected segments %v for a fresh session", names)
	}

	// Saved memory is fed back into the prompt (the recall path, not just the write).
	if err := st.WriteMemory("Daniel prefers tea."); err != nil {
		t.Fatal(err)
	}
	if names := segNames(sess.systemSegments()); !contains(names, "memory") {
		t.Errorf("system segments %v missing memory after WriteMemory", names)
	}

	// A persona (-system) leads the framing.
	sess.system = "You are terse."
	segs := sess.systemSegments()
	if len(segs) == 0 || segs[0].Name != "system" {
		t.Fatalf("segments = %v, want system first", segNames(segs))
	}
	if !strings.Contains(segs[0].Messages[0].Content, "terse") {
		t.Errorf("system segment = %q", segs[0].Messages[0].Content)
	}
}

func segNames(segs []contextbudget.Segment) []string {
	n := make([]string, len(segs))
	for i, s := range segs {
		n[i] = s.Name
	}
	return n
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// Resume reopens the project's most recent session with its history loaded (into both the
// model context and the display), /new starts a distinct empty session, and both show up
// in the session list — the terminal analogue of the desktop's multi-session loop.
func TestSessionResumeAndNew(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	// First session: one turn, persisted.
	s1, err := Open(be, st, dir, allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Turn(context.Background(), "Hi", nil); err != nil {
		t.Fatal(err)
	}
	firstID := s1.convID

	// Reopen with resume: the same session, its prior exchange loaded for context and
	// display.
	s2, err := Open(be, st, dir, allowPrompter{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if s2.convID != firstID {
		t.Errorf("resume opened %q, want the last session %q", s2.convID, firstID)
	}
	if len(s2.msgs) < 2 {
		t.Errorf("resumed msgs = %d, want the prior user + assistant", len(s2.msgs))
	}
	if len(s2.HistoryItems()) == 0 {
		t.Error("resumed history produced no display items")
	}

	// /new starts a fresh session lazily — nothing on disk until the first message.
	if err := s2.New(); err != nil {
		t.Fatal(err)
	}
	if s2.convID != "" {
		t.Errorf("New left convID=%q, want empty (lazy)", s2.convID)
	}
	if len(s2.msgs) != 0 {
		t.Errorf("new session msgs = %d, want 0", len(s2.msgs))
	}
	if ss, _ := s2.Sessions(); len(ss) != 1 {
		t.Errorf("sessions after lazy /new = %d, want 1 (New created nothing yet)", len(ss))
	}

	// A message creates the new session — now two distinct sessions.
	if err := s2.Turn(context.Background(), "Second", nil); err != nil {
		t.Fatal(err)
	}
	if s2.convID == "" || s2.convID == firstID {
		t.Errorf("new session convID=%q, want a fresh non-empty id", s2.convID)
	}
	sessions, err := s2.Sessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Errorf("sessions = %d, want 2", len(sessions))
	}
}

// With a Forge URL and at least one image model configured, Open wires the
// image-generation tool into the registry — generate_image is offered in the TUI exactly
// as in the desktop. No Forge connection happens until the tool is called, so a fake URL
// is fine here.
func TestSessionWiresImageTool(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetConfig(store.Config{ForgeURL: "http://forge.invalid:7860"}); err != nil {
		t.Fatal(err)
	}
	cfgJSON, err := json.Marshal(imagegen.Config{Default: "m", Models: []imagegen.ModelConfig{{Name: "m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.Root(), "imagegen.json"), cfgJSON, 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := Open(be, st, t.TempDir(), allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	found := false
	for _, tl := range sess.Tools() {
		if tl.Name == imagegen.ToolName {
			found = true
		}
	}
	if !found {
		t.Errorf("image tool %q not wired; registry has %d tools", imagegen.ToolName, len(sess.Tools()))
	}
}

// The image sink keeps the decoded image in memory (for /image and the auto-show) while
// still persisting it to the conversation's attachments.
func TestSessionSinkCapturesImage(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sess, err := Open(be, st, t.TempDir(), allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := sess.ensureSession(); err != nil { // the sink saves into a conversation
		t.Fatal(err)
	}

	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	ref, err := (sessionSink{s: sess}).Save(context.Background(), "gen.png", pngBuf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if ref != "attachments/gen.png" {
		t.Errorf("ref = %q, want attachments/gen.png", ref)
	}
	if len(sess.images) != 1 || sess.turnImages != 1 {
		t.Errorf("image not captured: images=%d turnImages=%d, want 1 each", len(sess.images), sess.turnImages)
	}
}

// Resuming a session recovers its saved images from the attachments folder into the viewer
// list — so /image works on a reopened chat, even one whose images predate the log-ref fix.
func TestSessionLoadsImagesOnResume(t *testing.T) {
	srv := lcpptest.NewServer()
	t.Cleanup(srv.Close)
	be := backend.New(srv.URL, backend.WithHTTPClient(srv.Client()))
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()

	// A session with content and a saved image.
	s1, err := Open(be, st, dir, allowPrompter{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Turn(context.Background(), "hi", nil); err != nil {
		t.Fatal(err)
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 3, 3))); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveAttachment(s1.convID, "pic.png", pngBuf.Bytes()); err != nil {
		t.Fatal(err)
	}

	// Resume: the saved image is recovered, so /image has something to show.
	s2, err := Open(be, st, dir, allowPrompter{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if s2.convID != s1.convID {
		t.Fatalf("resume opened %q, want %q", s2.convID, s1.convID)
	}
	if len(s2.images) != 1 {
		t.Errorf("resumed images = %d, want 1 (recovered from attachments)", len(s2.images))
	}
}

// An answer is split at its image reference so the picture renders inline between the text.
func TestAssistantItemsSplitsImages(t *testing.T) {
	if its := assistantItems("hello"); len(its) != 1 || its[0].kind != itemAssistant {
		t.Fatalf("plain text = %+v, want one assistant item", its)
	}
	its := assistantItems("Here you go\n\n![cat](attachments/c9.png)")
	if len(its) != 2 {
		t.Fatalf("split = %d items, want 2 (text + image)", len(its))
	}
	if its[0].kind != itemAssistant || !strings.Contains(its[0].text, "Here you go") {
		t.Errorf("first item = %+v, want the text", its[0])
	}
	if its[1].kind != itemImage || its[1].text != "c9.png" {
		t.Errorf("second item = %+v, want the image base name c9.png", its[1])
	}
}

// imageBlocks renders an image to colored half-block text (no raw markers, carries color).
func TestImageBlocks(t *testing.T) {
	out := imageBlocks(image.NewRGBA(image.Rect(0, 0, 8, 8)), 10, 10)
	if out == "" {
		t.Fatal("imageBlocks returned empty")
	}
	if !strings.Contains(out, "▀") {
		t.Errorf("output has no half-block char:\n%s", out)
	}
	if !strings.Contains(out, "[#") {
		t.Errorf("output has no color tags:\n%s", out)
	}
}

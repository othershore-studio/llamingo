// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package tui is the terminal face of llaminGO: a tview app that anchors a session
// to the current directory's project and runs the client-owned tool loop over the
// shared core, CLI-direct (no HTTP server). Turns stream a reply, the model may call
// the native tools (memory, time/location, project files) behind the permission gate
// — surfaced as a blocking terminal consent modal — and the trail is persisted like
// the desktop.
package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // register decoders so a generated image can be shown
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/filetools"
	"github.com/othershore-studio/llamingo/internal/imagegen"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
	"github.com/othershore-studio/llamingo/internal/toolreply"
)

const maxToolRounds = 8

const helpText = "\n[::d]commands:\n" +
	"  /new      start a fresh session\n" +
	"  /resume   open a past session\n" +
	"  /tools    turn tools on/off for this chat\n" +
	"  /memory   show what's in saved memory\n" +
	"  /prompt   show the system prompt sent to the model\n" +
	"  /thought  show the model's thinking for the last turn\n" +
	"  /image    view the last generated image\n" +
	"  /clear    clear the view\n" +
	"  /help     this list\n" +
	"  /exit     quit (or Ctrl+C)[-]\n"

// Session is a TUI chat anchored to a directory's project, driving turns over the core
// directly through the client-owned tool loop.
type Session struct {
	be     backend.Backend
	st     *store.Store
	reg    mcp.Registry
	convID string
	title  string
	dir    string
	projID store.ProjectID

	msgs       []backend.Message
	budget     contextbudget.Budget
	system     string
	lastTools  []string              // tool names from the most recent turn, for the display trail
	servers    []*mcp.ServerRegistry // connected MCP servers, closed on exit
	notes      []string              // startup notices (MCP / image status), shown once
	images     []image.Image         // all images generated this session, for the viewer
	turnImages int                   // how many were generated this turn
	lastThink  string                // the most recent turn's thinking, for /thought
}

// Open anchors a new session to dir's project (the same project the desktop opens) and
// wires the native tool registry — memory, time/location, and the project file tools
// (dir is the project root) — behind the permission gate, whose prompter is the
// caller's: a tview consent modal live, a fake in tests.
func Open(be backend.Backend, st *store.Store, dir string, prompter permission.Prompter, resume bool) (*Session, error) {
	dir = filepath.Clean(dir)
	proj, err := st.OpenProject(dir, filepath.Base(dir))
	if err != nil {
		return nil, fmt.Errorf("open project for %s: %w", dir, err)
	}
	s := &Session{be: be, st: st, title: filepath.Base(dir), dir: dir, projID: proj.ID}

	// The native tools, then the user's MCP servers and image generation — the same
	// toolset the desktop assembles (see httpapi.registryFor), so a tool works in both.
	// One gate (prompter + grants) is shared across the gated tools; global MCP servers
	// are user-authored and run trusted (nil gate), project .mcp.json is gated.
	gate := permission.New(prompter, permission.NewMemoryGrantStore())
	regs := []mcp.Registry{
		memtool.NewTool(st, gate),
		localtools.NewTool(func() string { c, _ := st.Config(); return c.Location }),
		filetools.NewTool(dir, gate, map[string]bool{}),
	}
	if g := s.connectServers(mcp.NewRegistry(nil), globalMCPPath(st.Root()), ""); g != nil {
		regs = append(regs, g)
	}
	if p := s.connectServers(mcp.NewRegistry(gate), "", projectMCPPath(dir)); p != nil {
		regs = append(regs, p)
	}
	imageOn := false
	if img := imageTool(st, s, gate); img != nil {
		regs = append(regs, img)
		imageOn = true
	}
	s.reg = mcp.Combine(regs...)
	s.summarizeTools(imageOn)

	// -c resumes the project's most recent session that has content, skipping the empty
	// shells a launch used to leave behind. Otherwise the session is created lazily on the
	// first message (ensureSession), so opening the TUI without chatting leaves nothing.
	if resume {
		if sessions, err := st.ListSessions(proj.ID); err == nil {
			for _, m := range sessions { // newest first
				if hasContent(m) {
					return s, s.Load(m.ID)
				}
			}
		}
	}
	return s, nil
}

// hasContent reports whether a session has been written to — an untouched one keeps
// Updated == Created, so this skips empty shells on resume and in the picker.
func hasContent(m store.ConversationMeta) bool { return !m.Updated.Equal(m.Created) }

// ensureSession creates the backing session on first use (lazy creation), so launching the
// TUI without sending a message leaves no empty session behind.
func (s *Session) ensureSession() error {
	if s.convID != "" {
		return nil
	}
	sess, err := s.st.NewSession(s.projID, "New session")
	if err != nil {
		return err
	}
	s.convID = sess.ID
	return nil
}

// connectServers loads one MCP config (global XOR project — pass "" for the other) and
// connects each declared server into reg, tracking it for Close. A server that won't
// start is noted and skipped, never fatal. Returns nil when none connected, so an empty
// registry isn't combined in.
func (s *Session) connectServers(reg *mcp.ServerRegistry, globalPath, projectPath string) *mcp.ServerRegistry {
	servers, err := mcp.Load(globalPath, projectPath)
	if err != nil {
		s.notes = append(s.notes, "[red]mcp: config error: "+tview.Escape(err.Error())+"[-]")
	}
	n := 0
	for _, srv := range servers {
		c, err := mcp.Connect(srv)
		if err != nil {
			s.notes = append(s.notes, "[yellow]mcp: couldn't start "+tview.Escape(srv.Name)+"[-]")
			continue
		}
		if err := reg.Add(context.Background(), srv, c); err != nil {
			s.notes = append(s.notes, "[yellow]mcp: "+tview.Escape(srv.Name)+" — "+tview.Escape(err.Error())+"[-]")
			_ = c.Close()
			continue
		}
		n++
	}
	if n == 0 {
		reg.Close()
		return nil
	}
	s.servers = append(s.servers, reg)
	return reg
}

// summarizeTools adds a one-line startup note of the MCP tools available and whether image
// generation is on, so parity is visible at a glance (the detail is in /tools).
func (s *Session) summarizeTools(imageOn bool) {
	mcpTools := 0
	for _, sr := range s.servers {
		mcpTools += len(sr.Tools())
	}
	if mcpTools == 0 && !imageOn {
		return
	}
	img := "off"
	if imageOn {
		img = "on"
	}
	s.notes = append(s.notes, fmt.Sprintf("[::d]tools: %d from MCP · image generation %s[-]", mcpTools, img))
}

// Close shuts the session's MCP server connections. Called once, on exit.
func (s *Session) Close() {
	for _, sr := range s.servers {
		sr.Close()
	}
	s.servers = nil
}

// imageTool builds the image-generation tool when a Forge URL (config or
// LLAMINGO_FORGE_URL) and at least one model are configured, else nil. Its sink saves into
// the session's current conversation, resolved per call so /new and /resume follow.
func imageTool(st *store.Store, s *Session, gate *permission.Gate) mcp.Registry {
	cfg, _ := st.Config()
	url := cfg.ForgeURL
	if url == "" {
		url = os.Getenv("LLAMINGO_FORGE_URL")
	}
	if url == "" {
		return nil
	}
	imgCfg, err := imagegen.LoadConfig(filepath.Join(st.Root(), "imagegen.json"))
	if err != nil || len(imgCfg.Models) == 0 {
		return nil
	}
	return imagegen.NewTool(imagegen.NewForgeNeo(url), sessionSink{s}, gate, imgCfg)
}

// sessionSink saves generated image bytes into the session's current conversation.
type sessionSink struct{ s *Session }

func (k sessionSink) Save(ctx context.Context, name string, data []byte) (string, error) {
	ref, err := k.s.st.SaveAttachment(k.s.convID, name, data)
	if err == nil {
		// Keep the decoded image in memory for the terminal viewer (/image, and the
		// auto-show after a generating turn). A non-image or undecodable payload is just
		// saved, not shown.
		if img, _, derr := image.Decode(bytes.NewReader(data)); derr == nil {
			k.s.images = append(k.s.images, img)
			k.s.turnImages++
		}
	}
	return ref, err
}

// globalMCPPath is the global MCP config in the store root (mcp.json, or a copied-in
// .mcp.json), mirroring httpapi so the TUI and desktop read the same servers.
func globalMCPPath(root string) string {
	plain := filepath.Join(root, "mcp.json")
	if _, err := os.Stat(plain); err == nil {
		return plain
	}
	dotted := filepath.Join(root, ".mcp.json")
	if _, err := os.Stat(dotted); err == nil {
		return dotted
	}
	return plain
}

func projectMCPPath(root string) string { return filepath.Join(root, ".mcp.json") }

// Load replaces the session's conversation with an existing one's stored history — the
// folded messages for context (store.Project drops display-only reasoning and the tool
// trail, so thinking never re-enters the prompt).
func (s *Session) Load(convID string) error {
	entries, err := s.st.LoadConversation(convID)
	if err != nil {
		return err
	}
	s.convID = convID
	s.msgs = messagesFrom(store.Project(entries))
	s.lastTools = nil
	s.lastThink = lastReasoning(entries)
	s.loadImages(convID)
	return nil
}

// lastReasoning is the most recent captured thinking in a conversation (for /thought after
// a resume), or empty if none.
func lastReasoning(entries []store.Entry) string {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Kind == store.KindReasoning {
			return entries[i].Content
		}
	}
	return ""
}

// loadImages recovers a conversation's saved images from its attachments folder into the
// in-memory viewer list, so /image works after a resume (the sink only fills the list
// during live turns). Non-image files and decode failures are skipped.
func (s *Session) loadImages(convID string) {
	s.images = nil
	names, err := s.st.ListAttachments(convID)
	if err != nil {
		return
	}
	for _, name := range names {
		p, err := s.st.AttachmentPath(convID, name)
		if err != nil {
			continue
		}
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		img, _, derr := image.Decode(f)
		_ = f.Close()
		if derr == nil {
			s.images = append(s.images, img)
		}
	}
}

// resolveImage loads an attachment image by base name for inline rendering, decoded fresh
// each call (cheap for chat-sized images).
func (s *Session) resolveImage(name string) image.Image {
	p, err := s.st.AttachmentPath(s.convID, name)
	if err != nil {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	img, _, _ := image.Decode(f)
	return img
}

// New starts a fresh session: lazily, so nothing is written to disk until the first
// message (ensureSession), keeping empty sessions out of the project.
func (s *Session) New() error {
	s.convID = ""
	s.msgs = nil
	s.lastTools = nil
	s.images = nil
	s.lastThink = ""
	s.budget = contextbudget.Budget{}
	return nil
}

// Sessions lists this project's saved sessions, newest first — for the /resume picker.
func (s *Session) Sessions() ([]store.ConversationMeta, error) {
	return s.st.ListSessions(s.projID)
}

// HistoryItems renders the current session's stored conversation as display items —
// user/assistant messages, the tool trail, and any compaction summary — for repainting
// on resume. Reasoning traces are display-only thinking and left out.
func (s *Session) HistoryItems() []item {
	entries, err := s.st.LoadConversation(s.convID)
	if err != nil {
		return nil
	}
	return historyItems(entries)
}

// messagesFrom projects stored entries to model messages (mirrors httpapi's
// messagesFromEntries): a compaction summary stands in as a system note, the rest carry
// their role. Call it with store.Project(entries) so folded and display-only entries drop.
func messagesFrom(entries []store.Entry) []backend.Message {
	msgs := make([]backend.Message, 0, len(entries))
	for _, e := range entries {
		if e.Kind == store.KindSummary {
			msgs = append(msgs, backend.Message{Role: backend.RoleSystem, Content: "Summary of earlier conversation:\n" + e.Content})
			continue
		}
		msgs = append(msgs, backend.Message{Role: backend.Role(e.Role), Content: e.Content})
	}
	return msgs
}

// historyItems maps a conversation's raw entries to display items: user/assistant
// messages, the tool trail (name in Content), and a compaction summary as a dim notice.
// Reasoning traces are display-only thinking and skipped on reload.
func historyItems(entries []store.Entry) []item {
	var items []item
	for _, e := range entries {
		switch e.Kind {
		case store.KindMessage:
			switch e.Role {
			case "user":
				items = append(items, item{itemUser, e.Content})
			case "assistant":
				items = append(items, assistantItems(e.Content)...)
			}
		case store.KindToolCall:
			items = append(items, item{itemTool, e.Name}) // the tool name (args live in e.Content)
		case store.KindSummary:
			items = append(items, item{itemNotice, "[::d]— summary of earlier conversation —[-]\n[::d]" + tview.Escape(e.Content) + "[-]"})
		}
	}
	return items
}

// Turn runs one exchange through mcp.Run: the model may call the native tools, each
// mediated by the gate. It persists the trail (thinking, tool uses, answer) like the
// desktop — thinking and uses are display-only, the answer is the durable turn — and
// reports each delta to onDelta for rendering.
func (s *Session) Turn(ctx context.Context, input string, onDelta func(backend.Delta) error) error {
	input = strings.TrimSpace(input)
	if input == "" {
		return nil
	}
	if err := s.ensureSession(); err != nil {
		return err
	}
	s.lastTools = nil
	s.turnImages = 0
	s.msgs = append(s.msgs, backend.Message{Role: backend.RoleUser, Content: input})
	if _, err := s.st.AppendMessage(s.convID, "user", input); err != nil {
		return err
	}

	bld := contextbudget.New(s.be)
	segs := s.segments()
	if b, err := bld.Estimate(ctx, s.budgetSegments()); err == nil {
		s.budget = b
	}

	var think strings.Builder
	var timings *backend.Timings
	reg := mcp.Curate(s.reg, s.disabledSet())
	out, err := mcp.Run(ctx, s.be, reg, bld.Messages(segs), func(d backend.Delta) error {
		if d.Reasoning != "" {
			think.WriteString(d.Reasoning)
		}
		if d.Usage != nil && d.Usage.PromptTokens > s.budget.Authoritative {
			s.budget.Authoritative = d.Usage.PromptTokens
		}
		if d.Timings != nil {
			timings = d.Timings
		}
		if onDelta != nil {
			return onDelta(d)
		}
		return nil
	}, maxToolRounds, nil)
	if err != nil {
		return err
	}
	s.lastThink = think.String()

	// Shape the saved answer like the desktop does (internal/toolreply): strip ReAct
	// scratch, and recover an image from the tool result when the model forgot to show it —
	// so the image reference lands in the log and the picture also shows in the desktop.
	answer := toolreply.SurfaceImageDenial(toolreply.RecoverToolImages(toolreply.StripLeadingToolBlob(finalAssistant(out)), out), out)
	s.msgs = append(s.msgs, backend.Message{Role: backend.RoleAssistant, Content: answer})

	if think.Len() > 0 {
		_ = s.st.AppendReasoning(s.convID, think.String())
	}
	var toolNames []string
	for _, tc := range toolCallsOf(out) {
		_ = s.st.AppendToolUse(s.convID, tc.name, tc.args)
		toolNames = append(toolNames, tc.name)
	}
	s.lastTools = toolNames
	tps := 0.0
	if timings != nil {
		tps = timings.PredictPerSecond
	}
	_, err = s.st.AppendMessageTimed(s.convID, "assistant", answer, tps)
	return err
}

func (s *Session) segments() []contextbudget.Segment {
	return append(s.systemSegments(), contextbudget.Segment{Name: "conversation", Messages: s.msgs})
}

// systemSegments is the system context prepended before the conversation: the persona
// system prompt (when set), the working-directory note, and the personal memory (when
// non-empty) — each a metered segment. It mirrors the desktop's segmentsFor (httpapi)
// so a terminal turn gets the same framing — the model knows its cwd and recalls saved
// memory — and /prompt can show exactly what's sent.
func (s *Session) systemSegments() []contextbudget.Segment {
	var segs []contextbudget.Segment
	if strings.TrimSpace(s.system) != "" {
		segs = append(segs, contextbudget.Segment{Name: "system", Messages: []backend.Message{{Role: backend.RoleSystem, Content: s.system}}})
	}
	if note := s.workdirNote(); note != "" {
		segs = append(segs, contextbudget.Segment{Name: "workdir", Messages: []backend.Message{{Role: backend.RoleSystem, Content: note}}})
	}
	if mem, _, err := s.st.Memory(); err == nil && strings.TrimSpace(mem) != "" {
		segs = append(segs, contextbudget.Segment{Name: "memory", Messages: []backend.Message{{Role: backend.RoleSystem, Content: "Personal memory:\n" + mem}}})
	}
	return segs
}

// budgetSegments is what to meter for the status bar: the system framing, the offered
// tools (their schemas are a big chunk the chat template injects — so the context reads
// honestly, not short), then the conversation. The tools go in here only, not into the
// real turn (mcp.Run passes them to the backend); mirrors the desktop's budgetTools.
func (s *Session) budgetSegments() []contextbudget.Segment {
	segs := s.systemSegments()
	if tools := mcp.Curate(s.reg, s.disabledSet()).Tools(); len(tools) > 0 {
		if b, err := json.Marshal(tools); err == nil {
			segs = append(segs, contextbudget.Segment{Name: "tools", Messages: []backend.Message{{Role: backend.RoleSystem, Content: "Tools available:\n" + string(b)}}})
		}
	}
	return append(segs, contextbudget.Segment{Name: "conversation", Messages: s.msgs})
}

// workdirNote tells the model its working directory so it refers to files by relative
// paths (the file tools are confined to it). Empty when the project opts out.
func (s *Session) workdirNote() string {
	if show, err := s.st.ProjectShowWorkingDir(s.dir); err == nil && !show {
		return ""
	}
	return "Your working directory is " + s.dir + " — refer to files by paths relative to it."
}

// Budget is the latest metered budget (for the status bar).
func (s *Session) Budget() contextbudget.Budget { return s.budget }

// Tools is the full native toolset offered in this project, before per-chat curation —
// the set the /tools view toggles over.
func (s *Session) Tools() []backend.ToolDef { return s.reg.Tools() }

// toolGroup is a tool paired with where it comes from, for the /tools view.
type toolGroup struct {
	def   backend.ToolDef
	label string // "system" or "mcp · <server>"
}

// GroupedTools returns the toolset labeled by source and ordered System-first, then each
// MCP server's — so /tools makes clear what is native and what a server provides.
func (s *Session) GroupedTools() []toolGroup {
	mcpOf := map[string]string{}
	for _, sr := range s.servers {
		for _, g := range sr.ToolsByServer() {
			for _, t := range g.Tools {
				mcpOf[t.Name] = g.Server
			}
		}
	}
	tools := s.reg.Tools()
	out := make([]toolGroup, len(tools))
	for i, t := range tools {
		label := "system"
		if srv := mcpOf[t.Name]; srv != "" {
			label = "mcp · " + srv
		}
		out[i] = toolGroup{def: t, label: label}
	}
	sort.SliceStable(out, func(i, j int) bool {
		si, sj := out[i].label == "system", out[j].label == "system"
		if si != sj {
			return si // System tools first
		}
		return out[i].label < out[j].label // then grouped by MCP server name
	})
	return out
}

// Memory is the personal memory and whether it was last edited outside the app.
func (s *Session) Memory() (string, bool, error) { return s.st.Memory() }

// OfferedTools is the curated tool names a turn would actually offer the model (after
// /tools curation) — what /prompt lists as going out with the request.
func (s *Session) OfferedTools() []string {
	tools := mcp.Curate(s.reg, s.disabledSet()).Tools()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	return names
}

// disabledSet reads the tools curated off for this chat, fresh from the store, so a
// toggle in the /tools view takes effect on the next turn (matching the desktop; see
// mcp.Curate, which expects the disabled set supplied per turn).
func (s *Session) disabledSet() map[string]bool {
	cur, _ := s.st.ConversationTools(s.convID)
	if len(cur) == 0 {
		return nil
	}
	set := make(map[string]bool, len(cur))
	for _, n := range cur {
		set[n] = true
	}
	return set
}

// ToggleTool flips one tool's curation for this chat and persists it, reporting
// whether the tool is now disabled.
func (s *Session) ToggleTool(name string) (disabled bool, err error) {
	if err := s.ensureSession(); err != nil { // curation is per-chat, so it needs a chat
		return false, err
	}
	set := s.disabledSet()
	if set == nil {
		set = map[string]bool{}
	}
	if set[name] {
		delete(set, name)
	} else {
		set[name] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return set[name], s.st.SetConversationTools(s.convID, out)
}

// finalAssistant is the last assistant message's text in a turn's output.
func finalAssistant(msgs []backend.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == backend.RoleAssistant && msgs[i].Content != "" {
			return msgs[i].Content
		}
	}
	return ""
}

type toolUse struct{ name, args string }

func toolCallsOf(msgs []backend.Message) []toolUse {
	var uses []toolUse
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			uses = append(uses, toolUse{name: tc.Function.Name, args: tc.Function.Arguments})
		}
	}
	return uses
}

// prompter implements permission.Prompter as a blocking tview modal: the tool loop
// (on a worker goroutine) calls Ask, which shows a consent modal on the UI goroutine
// and blocks on the user's choice — the terminal analogue of the desktop SSE handshake.
type prompter struct {
	app     *tview.Application
	pages   *tview.Pages
	restore tview.Primitive
}

func (p *prompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	res := make(chan permission.Decision, 1)
	labels := make([]string, 0, len(allowed)+1)
	for _, sc := range allowed {
		labels = append(labels, scopeLabel(sc))
	}
	labels = append(labels, "Deny")
	text := req.Label
	if req.Detail != "" {
		text += "\n\n" + req.Detail
	}

	p.app.QueueUpdateDraw(func() {
		modal := tview.NewModal().SetText(text).AddButtons(labels).
			SetDoneFunc(func(i int, _ string) {
				p.pages.RemovePage("consent")
				if p.restore != nil {
					p.app.SetFocus(p.restore)
				}
				if i >= 0 && i < len(allowed) {
					res <- permission.Decision{Allow: true, Scope: allowed[i]}
					return
				}
				res <- permission.Decision{Allow: false}
			})
		p.pages.AddPage("consent", modal, true, true)
	})

	select {
	case dec := <-res:
		return dec, nil
	case <-ctx.Done():
		return permission.Decision{}, ctx.Err()
	}
}

func scopeLabel(s permission.Scope) string {
	switch s {
	case permission.ScopeOnce:
		return "Once"
	case permission.ScopeSession:
		return "This chat"
	case permission.ScopePersistent:
		return "Always"
	}
	return "Allow"
}

// Run opens a session for dir and runs the interactive tview app until the user quits.
// system, when non-empty (the -system flag), is the persona system prompt prepended to
// every turn — shown by /prompt. resume (-c) opens the project's most recent session
// instead of a fresh one.
func Run(be backend.Backend, st *store.Store, dir, system string, resume bool) error {
	app := tview.NewApplication()
	pages := tview.NewPages()
	transcript := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	status := tview.NewTextView().SetDynamicColors(true)
	input := tview.NewTextArea().SetLabel("› ").SetPlaceholder("message — Enter sends · Ctrl+J for a newline")
	input.SetWrap(true).SetWordWrap(true)

	sess, err := Open(be, st, dir, &prompter{app: app, pages: pages, restore: input}, resume)
	if err != nil {
		return err
	}
	defer sess.Close()
	sess.system = system
	model, nctx := "unknown model", 0
	if props, perr := be.Props(context.Background()); perr == nil {
		model, nctx = modelName(props.ModelID), props.NCtx
	}
	// Prefer the active connection's alias (what the desktop shows) over the raw model file.
	label := model
	if cfg, err := st.Config(); err == nil {
		for _, c := range cfg.Connections {
			if c.ID == cfg.ActiveID && strings.TrimSpace(c.Name) != "" {
				label = c.Name
				break
			}
		}
	}

	setStatus := func() {
		b := sess.Budget()
		status.Clear()
		// Colour the context by fill level — a calm→amber→red cue toward the limit.
		ctxColor := "green"
		switch b.Level() {
		case contextbudget.LevelNear:
			ctxColor = "yellow"
		case contextbudget.LevelFull:
			ctxColor = "red"
		}
		fmt.Fprintf(status, "[::d]%s · %s · [-][%s]ctx %d/%d[-]", sess.title, tview.Escape(label), ctxColor, b.Used(), nctx)
	}
	setStatus()

	welcome := fmt.Sprintf("[::d]llaminGO · %s · %s — type a message, /help for commands, Ctrl+C or /exit to quit[-]\n", sess.title, label)
	v := &view{tv: transcript, welcome: welcome, resolve: sess.resolveImage}

	// Re-render inline images when the transcript width changes (the first draw, or a
	// resize), so they fit the real width instead of the pre-draw placeholder.
	lastWidth := 0
	transcript.SetDrawFunc(func(_ tcell.Screen, _, _, width, _ int) (int, int, int, int) {
		if width != lastWidth {
			lastWidth = width
			go app.QueueUpdateDraw(v.render) // re-fit width-dependent content (bars, images)
		}
		return transcript.GetInnerRect()
	})

	// showTools overlays a scrollable toggle list of the project's native tools — a
	// filled dot is on, hollow off — so a small model's toolset can be trimmed. Enter
	// toggles (persisted per-chat, applied next turn); Esc closes. The terminal face of
	// the desktop Tools panel.
	showTools := func() {
		groups := sess.GroupedTools()
		if len(groups) == 0 {
			v.notice("[::d]no tools in this project[-]")
			v.render()
			return
		}
		// Each row's secondary line names the source — "system" or "mcp · <server>" — so
		// it's clear what's native and what a server provides; the list is System-first.
		secondary := func(g toolGroup) string {
			s := g.label
			if d := firstLine(g.def.Description); d != "" {
				s += " · " + truncate(d, 44)
			}
			return tview.Escape(s)
		}
		list := tview.NewList().ShowSecondaryText(true)
		list.SetBorder(true).SetTitle(" tools — system / mcp · Enter toggles · Esc closes ")
		for _, g := range groups {
			list.AddItem(mark(sess.disabledSet()[g.def.Name])+" "+g.def.Name, secondary(g), 0, nil)
		}
		list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
			nowDisabled, _ := sess.ToggleTool(groups[i].def.Name)
			list.SetItemText(i, mark(nowDisabled)+" "+groups[i].def.Name, secondary(groups[i]))
		})
		list.SetDoneFunc(func() {
			pages.RemovePage("tools")
			app.SetFocus(input)
			off := len(sess.disabledSet())
			v.notice(fmt.Sprintf("[::d]tools: %d of %d on[-]", len(groups)-off, len(groups)))
			v.render()
		})
		pages.AddPage("tools", centeredModal(list), true, true)
		app.SetFocus(list)
	}

	busy := false

	// reload repaints the transcript from the current session's stored history (after a
	// /new or /resume, and on startup) and re-meters the budget in the background, so the
	// status bar reflects the loaded context without blocking on a tokenize round-trip.
	reload := func() {
		v.items = sess.HistoryItems()
		if n := len(sess.images); n > 0 { // a resumed chat's saved images are viewable
			noun := "image"
			if n > 1 {
				noun = "images"
			}
			v.notice(fmt.Sprintf("[::d]%d %s in this chat · /image to view[-]", n, noun))
		}
		setStatus()
		v.render()
		// Meter the context — tools included — in the background so the status bar reflects
		// the real size without blocking on the tokenize round-trip.
		segs := sess.budgetSegments()
		go func() {
			if b, err := contextbudget.New(be).Estimate(context.Background(), segs); err == nil {
				app.QueueUpdateDraw(func() {
					if !busy { // a turn would re-meter; don't clobber one in flight
						sess.budget = b
						setStatus()
					}
				})
			}
		}()
	}

	// showResume overlays a picker of the project's saved sessions (newest first); Enter
	// opens one, Esc cancels. The terminal face of the desktop session list.
	showResume := func() {
		all, err := sess.Sessions()
		if err != nil {
			v.notice("[red]resume: " + tview.Escape(err.Error()) + "[-]")
			v.render()
			return
		}
		var sessions []store.ConversationMeta
		for _, m := range all {
			if hasContent(m) { // skip the empty shells
				sessions = append(sessions, m)
			}
		}
		if len(sessions) == 0 {
			v.notice("[::d]no saved sessions yet[-]")
			v.render()
			return
		}
		list := tview.NewList().ShowSecondaryText(true)
		list.SetBorder(true).SetTitle(" resume — Enter opens · Esc cancels ")
		for _, m := range sessions {
			title := strings.TrimSpace(m.Title)
			if title == "" {
				title = "(untitled)"
			}
			marker := "  "
			if m.ID == sess.convID {
				marker = "▸ " // the session open now
			}
			list.AddItem(marker+tview.Escape(title), tview.Escape(m.Updated.Local().Format("Jan 2 15:04")), 0, nil)
		}
		list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
			pages.RemovePage("resume")
			app.SetFocus(input)
			if err := sess.Load(sessions[i].ID); err != nil {
				v.notice("[red]resume: " + tview.Escape(err.Error()) + "[-]")
				v.render()
				return
			}
			reload()
		})
		list.SetDoneFunc(func() {
			pages.RemovePage("resume")
			app.SetFocus(input)
		})
		pages.AddPage("resume", centeredModal(list), true, true)
		app.SetFocus(list)
	}

	// Image viewer: a reused Image primitive over a slice of images, navigated with ←/→
	// from the global key capture (the primitive has no key handling of its own). Drawn as
	// colored blocks, or sixel where the terminal supports it.
	viewer := tview.NewImage().SetColors(tview.TrueColor)
	viewer.SetBorder(true)
	var viewerImgs []image.Image
	viewerIdx := 0
	drawViewer := func() {
		viewer.SetImage(viewerImgs[viewerIdx])
		hint := "Esc closes"
		if len(viewerImgs) > 1 {
			hint = "←/→ browse · Esc closes"
		}
		viewer.SetTitle(fmt.Sprintf(" image %d/%d — %s ", viewerIdx+1, len(viewerImgs), hint))
	}
	showImages := func(imgs []image.Image, start int) {
		if len(imgs) == 0 {
			return
		}
		viewerImgs = imgs
		viewerIdx = start
		if viewerIdx < 0 || viewerIdx >= len(imgs) {
			viewerIdx = len(imgs) - 1
		}
		drawViewer()
		pages.AddPage("image", centeredModal(viewer), true, true)
		app.SetFocus(viewer)
	}

	// showThought overlays a scrollable window of the last turn's captured thinking, so you
	// can read what the model reasoned before answering. Esc closes it.
	showThought := func() {
		if strings.TrimSpace(sess.lastThink) == "" {
			v.notice("[::d]no thinking captured for the last turn[-]")
			v.render()
			return
		}
		tv := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true).SetWordWrap(true)
		tv.SetBorder(true).SetTitle(" thinking — Esc closes · ↑/↓ scroll ")
		fmt.Fprintf(tv, "[::d]%s[-]", tview.Escape(sess.lastThink))
		pages.AddPage("thought", centeredModal(tv), true, true)
		app.SetFocus(tv)
	}

	submit := func() {
		if busy {
			return
		}
		text := strings.TrimSpace(input.GetText())
		if text == "" {
			return
		}
		input.SetText("", false)
		if strings.HasPrefix(text, "/") {
			switch strings.ToLower(strings.Fields(text)[0]) {
			case "/exit", "/quit", "/q":
				app.Stop()
				return
			case "/tools":
				showTools()
				return
			case "/new":
				if err := sess.New(); err != nil {
					v.notice("[red]new session: " + tview.Escape(err.Error()) + "[-]")
					v.render()
					return
				}
				reload()
				return
			case "/resume":
				showResume()
				return
			case "/image":
				if len(sess.images) == 0 {
					v.notice("[::d]no image yet — ask the model to generate one[-]")
					v.render()
				} else {
					showImages(sess.images, len(sess.images)-1)
				}
				return
			case "/thought", "/thinking":
				showThought()
				return
			case "/memory":
				mem, tampered, err := sess.Memory()
				if err != nil {
					v.notice("[red]memory: " + tview.Escape(err.Error()) + "[-]")
				} else {
					title := "saved memory"
					if tampered {
						title += " — edited outside the app"
					}
					v.notice(blockString(title, mem))
				}
			case "/prompt", "/system":
				var sb strings.Builder
				segs := sess.systemSegments()
				if len(segs) == 0 {
					sb.WriteString("[::d]system prompt: none — the model sees only the conversation[-]")
				} else {
					sb.WriteString("[::d]system prompt sent before the conversation:[-]")
					for _, seg := range segs {
						for _, m := range seg.Messages {
							sb.WriteString(blockString(seg.Name, m.Content))
						}
					}
				}
				if names := sess.OfferedTools(); len(names) > 0 {
					sb.WriteString(blockString("tools offered", strings.Join(names, ", ")))
				}
				v.notice(sb.String())
			case "/help", "/?":
				v.notice(helpText)
			case "/clear":
				v.items = nil
			default:
				v.notice("[red]unknown command " + tview.Escape(strings.Fields(text)[0]) + " — try /help[-]")
			}
			v.render()
			return
		}
		busy = true
		v.add(item{itemUser, text})
		v.render()
		// Stream the reply raw as it arrives, for responsiveness; the completion repaint
		// upgrades it to rendered markdown.
		fmt.Fprint(transcript, "[white::b]♦[-:-:-]  ")
		transcript.ScrollToEnd()

		// Animate a "thinking…" spinner until the reply lands, so the pre-token gap
		// (prompt processing / reasoning / a tool running) doesn't look dead.
		spinStop := make(chan struct{})
		go spinner(app, status, label, spinStop)

		go func() {
			rerr := sess.Turn(context.Background(), text, func(d backend.Delta) error {
				if d.Content != "" {
					app.QueueUpdateDraw(func() {
						fmt.Fprint(transcript, tview.Escape(d.Content))
						transcript.ScrollToEnd()
					})
				}
				if d.Done {
					for _, tc := range d.ToolCalls {
						name := tc.Function.Name
						app.QueueUpdateDraw(func() {
							fmt.Fprintf(transcript, "\n[yellow::d]▸ used %s[-:-:-]\n[white::b]♦[-:-:-]  ", tview.Escape(name))
						})
					}
				}
				return nil
			})
			close(spinStop)
			app.QueueUpdateDraw(func() {
				// Commit the turn to the display model and repaint: the raw streamed text
				// is replaced by the markdown-rendered answer, after the tool trail.
				for _, name := range sess.lastTools {
					v.add(item{itemTool, name})
				}
				if n := len(sess.msgs); n > 0 && sess.msgs[n-1].Role == backend.RoleAssistant {
					// Split the answer at any image reference so the picture renders inline.
					for _, it := range assistantItems(sess.msgs[n-1].Content) {
						v.add(it)
					}
				}
				if rerr != nil {
					v.notice("[red]error: " + tview.Escape(rerr.Error()) + "[-]")
				}
				v.render()
				setStatus()
				busy = false
				if sess.turnImages > 0 { // it renders inline above; offer the crisp view
					v.notice("[::d]· /image for a full-resolution view[-]")
					v.render()
				}
			})
		}()
	}

	// Composer keys: Enter sends; Ctrl+J (and Shift/Alt+Enter where the terminal reports
	// the modifier) inserts a newline. The composer is a multi-line tview TextArea.
	input.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyEnter:
			if ev.Modifiers()&(tcell.ModShift|tcell.ModAlt) != 0 {
				return tcell.NewEventKey(tcell.KeyEnter, '\n', tcell.ModNone) // newline
			}
			submit()
			return nil // plain Enter sends
		case tcell.KeyCtrlJ:
			return tcell.NewEventKey(tcell.KeyEnter, '\n', tcell.ModNone) // newline
		}
		return ev
	})

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlC:
			app.Stop()
			return nil
		case tcell.KeyEscape:
			// The image and thought views have no key handling of their own; close here.
			if name, _ := pages.GetFrontPage(); name == "image" || name == "thought" {
				pages.RemovePage(name)
				app.SetFocus(input)
				return nil
			}
			return ev
		case tcell.KeyLeft, tcell.KeyRight:
			// Browse the open image viewer; otherwise the input field keeps its cursor keys.
			if name, _ := pages.GetFrontPage(); name == "image" {
				if ev.Key() == tcell.KeyLeft && viewerIdx > 0 {
					viewerIdx--
					drawViewer()
				} else if ev.Key() == tcell.KeyRight && viewerIdx < len(viewerImgs)-1 {
					viewerIdx++
					drawViewer()
				}
				return nil
			}
			return ev
		case tcell.KeyPgUp, tcell.KeyPgDn:
			// Scroll the transcript a page while the input keeps focus — but only on the
			// main view, so a modal's own paging (the tools list) still works.
			if name, _ := pages.GetFrontPage(); name != "main" {
				return ev
			}
			row, col := transcript.GetScrollOffset()
			_, _, _, h := transcript.GetInnerRect()
			page := h - 1
			if page < 1 {
				page = 1
			}
			if ev.Key() == tcell.KeyPgUp {
				row -= page
			} else {
				row += page
			}
			if row < 0 {
				row = 0
			}
			transcript.ScrollTo(row, col)
			return nil
		}
		return ev
	})

	// A thin full-width rule above and below the composer to set it off.
	rule := func() *tview.TextView {
		r := tview.NewTextView().SetDynamicColors(true)
		r.SetWrap(false).SetText("[#3a3a3a]" + strings.Repeat("─", 500) + "[-]")
		return r
	}
	rows := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(transcript, 0, 1, false).
		AddItem(rule(), 1, 0, false).
		AddItem(input, 1, 0, true).
		AddItem(rule(), 1, 0, false).
		AddItem(status, 1, 0, false)
	// A one-column left margin so content isn't flush against the window edge.
	main := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, 1, 0, false).
		AddItem(rows, 0, 1, true).
		AddItem(nil, 1, 0, false)
	pages.AddPage("main", main, true, true)

	// Grow the composer with its content (newlines) up to a cap, so a multi-line message
	// stays visible as you type.
	input.SetChangedFunc(func() {
		lines := strings.Count(input.GetText(), "\n") + 1
		if lines > 8 {
			lines = 8
		}
		rows.ResizeItem(input, lines, 0)
	})

	reload()
	for _, n := range sess.notes {
		v.notice(n)
	}
	if len(sess.notes) > 0 {
		v.render()
	}
	app.EnableMouse(true) // wheel-scroll the transcript; click to position the cursor or pick a list item
	return app.SetRoot(pages, true).SetFocus(input).Run()
}

// spinner animates a "thinking…" indicator in the status bar until stop is closed —
// the terminal analogue of the desktop's working indicator.
func spinner(app *tview.Application, status *tview.TextView, model string, stop <-chan struct{}) {
	frames := []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")
	tk := time.NewTicker(90 * time.Millisecond)
	defer tk.Stop()
	for i := 0; ; i++ {
		select {
		case <-stop:
			return
		case <-tk.C:
			f := frames[i%len(frames)]
			app.QueueUpdateDraw(func() {
				status.Clear()
				fmt.Fprintf(status, "[yellow]%c[-:-:-]  thinking…  [::d]· %s[-:-:-]", f, model)
			})
		}
	}
}

// mark is the on/off glyph for a tool row: a filled dot when enabled, hollow when
// curated off.
func mark(disabled bool) string {
	if disabled {
		return "○"
	}
	return "●"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// centeredModal frames a primitive near-fullscreen with a small margin; the inner
// primitive (a scrollable list) handles overflow within its border. Shared by the tools
// and resume pickers.
func centeredModal(p tview.Primitive) tview.Primitive {
	return tview.NewFlex().
		AddItem(nil, 2, 0, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 1, 0, false).
			AddItem(p, 0, 1, true).
			AddItem(nil, 1, 0, false), 0, 1, true).
		AddItem(nil, 2, 0, false)
}

// blockString is a dimmed, titled block for the inspection commands (/memory, /prompt),
// returned as a string so it can be held as a notice item and survive the repaints.
func blockString(title, body string) string {
	head := fmt.Sprintf("\n[::d]── %s ──[-]\n", title)
	if strings.TrimSpace(body) == "" {
		return head + "[::d](empty)[-]"
	}
	return head + fmt.Sprintf("[::d]%s[-]", tview.Escape(body))
}

// item is one entry in the transcript's display model. The transcript is rebuilt from
// these after each turn so a streamed answer — shown raw as it arrives, for
// responsiveness — can be repainted as rendered markdown once complete. tview is
// append-only, so "upgrade the last answer" means repaint from the model.
type item struct {
	kind itemKind
	text string
}

type itemKind int

const (
	itemNotice    itemKind = iota // a pre-tagged system line (help, /memory, errors), verbatim
	itemUser                      // a user message
	itemTool                      // a tool the model used
	itemAssistant                 // an assistant answer, rendered as markdown
	itemImage                     // an inline image; text is its attachments/ base name
)

// view owns the transcript TextView and its display model.
type view struct {
	tv      *tview.TextView
	welcome string
	items   []item
	resolve func(name string) image.Image // loads an attachment image for inline rendering
}

func (v *view) add(it item)        { v.items = append(v.items, it) }
func (v *view) notice(text string) { v.add(item{itemNotice, text}) }

// drawUser paints the user turn as a full-width highlight bar (padded to the transcript
// width), one bar per line, with a blank line before and after so a turn is easy to find.
func (v *view) drawUser(text string) {
	_, _, w, _ := v.tv.GetInnerRect()
	fmt.Fprint(v.tv, "\n")
	for i, line := range strings.Split(text, "\n") {
		marker := " › "
		if i > 0 {
			marker = "   "
		}
		content := marker + line
		if w > 0 {
			if pad := w - len([]rune(content)); pad > 0 {
				content += strings.Repeat(" ", pad)
			}
		}
		fmt.Fprintf(v.tv, "[#e6edf3:#2d3f5e]%s[-:-]\n", tview.Escape(content))
	}
	fmt.Fprint(v.tv, "\n")
}

// render repaints the whole transcript from the model: markdown for answers, plain for
// the rest. Called after each change — cheap for a chat-sized history.
func (v *view) render() {
	v.tv.Clear()
	fmt.Fprint(v.tv, v.welcome)
	for _, it := range v.items {
		switch it.kind {
		case itemNotice:
			fmt.Fprintf(v.tv, "%s\n", it.text)
		case itemUser:
			v.drawUser(it.text)
		case itemTool:
			fmt.Fprintf(v.tv, "[yellow::d]▸ used %s[-:-:-]\n", tview.Escape(it.text))
		case itemAssistant:
			if body := renderMarkdown(it.text); body != "" {
				fmt.Fprintf(v.tv, "[white::b]♦[-:-:-]  %s\n", body)
			} else {
				fmt.Fprint(v.tv, "[white::b]♦[-:-:-]\n")
			}
		case itemImage:
			v.drawImage(it.text)
		}
	}
	v.tv.ScrollToEnd()
}

// drawImage renders an inline image as colored half-blocks sized to the transcript width
// (/image stays for a full-resolution modal view). A ref that can't be loaded degrades to
// a dim placeholder.
func (v *view) drawImage(name string) {
	_, _, w, _ := v.tv.GetInnerRect()
	if w <= 0 {
		// Width is unknown before the first draw; the draw hook re-renders once it is, so a
		// placeholder avoids painting at a wrong (tiny) size first.
		fmt.Fprint(v.tv, "[::d]  …[-]\n")
		return
	}
	var img image.Image
	if v.resolve != nil {
		img = v.resolve(name)
	}
	if img == nil {
		fmt.Fprintf(v.tv, "[::d](image: %s)[-]\n", tview.Escape(name))
		return
	}
	w -= 2
	if w > 96 {
		w = 96
	}
	if w < 8 {
		w = 8
	}
	fmt.Fprint(v.tv, imageBlocks(img, w, 28))
}

// imageBlocks renders an image as tview-tagged terminal text with the upper-half-block
// trick: each cell is two vertical pixels — foreground the top, background the bottom — so
// the vertical resolution doubles. The grid is maxW×maxH cells at most, preserving aspect
// (a cell is ~twice as tall as wide). Low-res by nature, but inline and scrolls with chat.
func imageBlocks(img image.Image, maxW, maxH int) string {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw <= 0 || ih <= 0 {
		return ""
	}
	w := maxW
	h := w * ih / (2 * iw)
	if h < 1 {
		h = 1
	}
	if h > maxH {
		h = maxH
		if w = h * 2 * iw / ih; w < 1 {
			w = 1
		} else if w > maxW {
			w = maxW
		}
	}
	var sb strings.Builder
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			sx := b.Min.X + col*iw/w
			tr, tg, tb := rgb8(img.At(sx, b.Min.Y+(2*row)*ih/(2*h)))
			br, bg, bb := rgb8(img.At(sx, b.Min.Y+(2*row+1)*ih/(2*h)))
			fmt.Fprintf(&sb, "[#%02x%02x%02x:#%02x%02x%02x]▀", tr, tg, tb, br, bg, bb)
		}
		sb.WriteString("[-:-]\n")
	}
	return sb.String()
}

func rgb8(c color.Color) (uint8, uint8, uint8) {
	r, g, b, _ := c.RGBA()
	return uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)
}

var attachImageRe = regexp.MustCompile(`!\[[^\]]*\]\(attachments/([^)\s]+)\)`)

// assistantItems splits an answer into text and inline-image items at each
// ![…](attachments/<name>) reference, so a generated image renders in place in the chat.
func assistantItems(content string) []item {
	locs := attachImageRe.FindAllStringSubmatchIndex(content, -1)
	if len(locs) == 0 {
		return []item{{itemAssistant, content}}
	}
	var items []item
	last := 0
	for _, loc := range locs {
		if pre := content[last:loc[0]]; strings.TrimSpace(pre) != "" {
			items = append(items, item{itemAssistant, pre})
		}
		items = append(items, item{itemImage, content[loc[2]:loc[3]]}) // group 1 = base name
		last = loc[1]
	}
	if rest := content[last:]; strings.TrimSpace(rest) != "" {
		items = append(items, item{itemAssistant, rest})
	}
	return items
}

func modelName(path string) string {
	if path == "" {
		return "unknown model"
	}
	name := path
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSuffix(name, ".gguf")
}

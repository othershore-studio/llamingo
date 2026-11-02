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
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/filetools"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
)

const maxToolRounds = 8

const helpText = "\n[::d]commands:\n" +
	"  /tools    turn tools on/off for this chat\n" +
	"  /memory   show what's in saved memory\n" +
	"  /prompt   show the system prompt sent to the model\n" +
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

	msgs   []backend.Message
	budget contextbudget.Budget
	system string
}

// Open anchors a new session to dir's project (the same project the desktop opens) and
// wires the native tool registry — memory, time/location, and the project file tools
// (dir is the project root) — behind the permission gate, whose prompter is the
// caller's: a tview consent modal live, a fake in tests.
func Open(be backend.Backend, st *store.Store, dir string, prompter permission.Prompter) (*Session, error) {
	dir = filepath.Clean(dir)
	proj, err := st.OpenProject(dir, filepath.Base(dir))
	if err != nil {
		return nil, fmt.Errorf("open project for %s: %w", dir, err)
	}
	sess, err := st.NewSession(proj.ID, "New session")
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	gate := permission.New(prompter, permission.NewMemoryGrantStore())
	reg := mcp.Combine(
		memtool.NewTool(st, gate),
		localtools.NewTool(func() string { return "" }),
		filetools.NewTool(dir, gate, map[string]bool{}),
	)
	return &Session{be: be, st: st, reg: reg, convID: sess.ID, title: filepath.Base(dir), dir: dir}, nil
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
	s.msgs = append(s.msgs, backend.Message{Role: backend.RoleUser, Content: input})
	if _, err := s.st.AppendMessage(s.convID, "user", input); err != nil {
		return err
	}

	bld := contextbudget.New(s.be)
	segs := s.segments()
	if b, err := bld.Estimate(ctx, segs); err == nil {
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

	answer := finalAssistant(out)
	s.msgs = append(s.msgs, backend.Message{Role: backend.RoleAssistant, Content: answer})

	if think.Len() > 0 {
		_ = s.st.AppendReasoning(s.convID, think.String())
	}
	for _, tc := range toolCallsOf(out) {
		_ = s.st.AppendToolUse(s.convID, tc.name, tc.args)
	}
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
// every turn — shown by /prompt.
func Run(be backend.Backend, st *store.Store, dir, system string) error {
	app := tview.NewApplication()
	pages := tview.NewPages()
	transcript := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	status := tview.NewTextView().SetDynamicColors(true)
	input := tview.NewInputField().SetLabel("› ")

	sess, err := Open(be, st, dir, &prompter{app: app, pages: pages, restore: input})
	if err != nil {
		return err
	}
	sess.system = system
	model, nctx := "unknown model", 0
	if props, perr := be.Props(context.Background()); perr == nil {
		model, nctx = modelName(props.ModelID), props.NCtx
	}

	setStatus := func() {
		b := sess.Budget()
		status.Clear()
		fmt.Fprintf(status, "[::d]%s · %s · ctx %d/%d[-]", sess.title, model, b.Used(), nctx)
	}
	setStatus()

	welcome := fmt.Sprintf("[::d]llaminGO · %s · %s — type a message, /help for commands, Ctrl+C or /exit to quit[-]\n", sess.title, model)

	// showTools overlays a scrollable toggle list of the project's native tools — a
	// filled dot is on, hollow off — so a small model's toolset can be trimmed. Enter
	// toggles (persisted per-chat, applied next turn); Esc closes. The terminal face of
	// the desktop Tools panel.
	showTools := func() {
		tools := sess.Tools()
		if len(tools) == 0 {
			fmt.Fprint(transcript, "\n[::d]no tools in this project[-]\n")
			transcript.ScrollToEnd()
			return
		}
		list := tview.NewList().ShowSecondaryText(true)
		list.SetBorder(true).SetTitle(" tools — Enter toggles · Esc closes ")
		for _, t := range tools {
			list.AddItem(mark(sess.disabledSet()[t.Name])+" "+t.Name, tview.Escape(truncate(firstLine(t.Description), 64)), 0, nil)
		}
		list.SetSelectedFunc(func(i int, _, _ string, _ rune) {
			nowDisabled, _ := sess.ToggleTool(tools[i].Name)
			list.SetItemText(i, mark(nowDisabled)+" "+tools[i].Name, tview.Escape(truncate(firstLine(tools[i].Description), 64)))
		})
		list.SetDoneFunc(func() {
			pages.RemovePage("tools")
			app.SetFocus(input)
			off := len(sess.disabledSet())
			fmt.Fprintf(transcript, "\n[::d]tools: %d of %d on[-]\n", len(tools)-off, len(tools))
			transcript.ScrollToEnd()
		})
		// Near-fullscreen with a small margin; the list scrolls inside its border.
		layout := tview.NewFlex().
			AddItem(nil, 2, 0, false).
			AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
				AddItem(nil, 1, 0, false).
				AddItem(list, 0, 1, true).
				AddItem(nil, 1, 0, false), 0, 1, true).
			AddItem(nil, 2, 0, false)
		pages.AddPage("tools", layout, true, true)
		app.SetFocus(list)
	}

	busy := false
	input.SetDoneFunc(func(key tcell.Key) {
		if key != tcell.KeyEnter || busy {
			return
		}
		text := strings.TrimSpace(input.GetText())
		if text == "" {
			return
		}
		input.SetText("")
		if strings.HasPrefix(text, "/") {
			switch strings.ToLower(strings.Fields(text)[0]) {
			case "/exit", "/quit", "/q":
				app.Stop()
			case "/tools":
				showTools()
				return
			case "/memory":
				mem, tampered, err := sess.Memory()
				if err != nil {
					fmt.Fprintf(transcript, "\n[red]memory: %s[-]\n", tview.Escape(err.Error()))
				} else {
					title := "saved memory"
					if tampered {
						title += " — edited outside the app"
					}
					printBlock(transcript, title, mem)
				}
				transcript.ScrollToEnd()
				return
			case "/prompt", "/system":
				segs := sess.systemSegments()
				if len(segs) == 0 {
					fmt.Fprint(transcript, "\n[::d]system prompt: none — the model sees only the conversation[-]\n")
				} else {
					fmt.Fprint(transcript, "\n[::d]system prompt sent before the conversation:[-]\n")
					for _, seg := range segs {
						for _, m := range seg.Messages {
							printBlock(transcript, seg.Name, m.Content)
						}
					}
				}
				if names := sess.OfferedTools(); len(names) > 0 {
					fmt.Fprintf(transcript, "[::d]── tools offered ──[-]\n[::d]%s[-]\n", tview.Escape(strings.Join(names, ", ")))
				}
				transcript.ScrollToEnd()
				return
			case "/help", "/?":
				fmt.Fprint(transcript, helpText)
			case "/clear":
				transcript.Clear()
				fmt.Fprint(transcript, welcome)
			default:
				fmt.Fprintf(transcript, "\n[red]unknown command %s — try /help[-]\n", tview.Escape(strings.Fields(text)[0]))
			}
			transcript.ScrollToEnd()
			return
		}
		busy = true
		fmt.Fprintf(transcript, "\n[blue::b]you[-:-:-]  %s\n[green::b]llama[-:-:-]  ", tview.Escape(text))
		transcript.ScrollToEnd()

		// Animate a "thinking…" spinner until the reply lands, so the pre-token gap
		// (prompt processing / reasoning / a tool running) doesn't look dead.
		spinStop := make(chan struct{})
		go spinner(app, status, model, spinStop)

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
							fmt.Fprintf(transcript, "\n[yellow::d]▸ used %s[-:-:-]\n[green::b]llama[-:-:-]  ", tview.Escape(name))
						})
					}
				}
				return nil
			})
			close(spinStop)
			app.QueueUpdateDraw(func() {
				if rerr != nil {
					fmt.Fprintf(transcript, "\n[red]error: %s[-]", tview.Escape(rerr.Error()))
				}
				fmt.Fprint(transcript, "\n")
				transcript.ScrollToEnd()
				setStatus()
				busy = false
			})
		}()
	})

	app.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		switch ev.Key() {
		case tcell.KeyCtrlC:
			app.Stop()
			return nil
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

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(transcript, 0, 1, false).
		AddItem(input, 1, 0, true).
		AddItem(status, 1, 0, false)
	pages.AddPage("main", flex, true, true)

	fmt.Fprint(transcript, welcome)
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

// printBlock writes a dimmed, titled block to the transcript — used by the inspection
// commands (/memory, /prompt) to show what the model is given, set off from the chat.
func printBlock(tv *tview.TextView, title, body string) {
	fmt.Fprintf(tv, "\n[::d]── %s ──[-]\n", title)
	if strings.TrimSpace(body) == "" {
		fmt.Fprint(tv, "[::d](empty)[-]\n")
		return
	}
	fmt.Fprintf(tv, "[::d]%s[-]\n", tview.Escape(body))
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

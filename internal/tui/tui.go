// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package tui is the terminal face of llaminGO: a tview app that anchors a session
// to the current directory's project and streams a chat over the shared core,
// CLI-direct (no HTTP server). This is the floor — an anchored streaming chat with
// the honest budget; the client-owned tool loop (mcp.Run) and the terminal
// permission modal are the next increment.
package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/store"
)

// Session is a TUI chat anchored to a directory's project. It drives turns over the
// core directly: persist the user message, stream the reply, persist it, re-meter.
type Session struct {
	be     backend.Backend
	st     *store.Store
	convID string
	title  string
	system string

	msgs   []backend.Message
	budget contextbudget.Budget
}

// Open anchors a new session to dir's project (creating the project if new) and
// returns it ready for turns — the same project the desktop opens for that folder.
func Open(be backend.Backend, st *store.Store, dir string) (*Session, error) {
	dir = filepath.Clean(dir)
	proj, err := st.OpenProject(dir, filepath.Base(dir))
	if err != nil {
		return nil, fmt.Errorf("open project for %s: %w", dir, err)
	}
	sess, err := st.NewSession(proj.ID, "New session")
	if err != nil {
		return nil, fmt.Errorf("new session: %w", err)
	}
	return &Session{be: be, st: st, convID: sess.ID, title: filepath.Base(dir)}, nil
}

// Turn runs one exchange: persist the user message, stream the reply (reporting each
// delta to onDelta), persist the reply, and re-meter the budget. Engine-agnostic, so
// it runs against the fake backend in tests.
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

	var reply strings.Builder
	err := s.be.ChatStream(ctx, backend.ChatRequest{Messages: bld.Messages(segs)}, func(d backend.Delta) error {
		if d.Content != "" {
			reply.WriteString(d.Content)
		}
		if d.Usage != nil && d.Usage.PromptTokens > s.budget.Authoritative {
			s.budget.Authoritative = d.Usage.PromptTokens
		}
		if onDelta != nil {
			return onDelta(d)
		}
		return nil
	})
	if err != nil {
		return err
	}

	answer := reply.String()
	s.msgs = append(s.msgs, backend.Message{Role: backend.RoleAssistant, Content: answer})
	_, err = s.st.AppendMessage(s.convID, "assistant", answer)
	return err
}

func (s *Session) segments() []contextbudget.Segment {
	var segs []contextbudget.Segment
	if s.system != "" {
		segs = append(segs, contextbudget.Segment{
			Name:     "system",
			Messages: []backend.Message{{Role: backend.RoleSystem, Content: s.system}},
		})
	}
	return append(segs, contextbudget.Segment{Name: "conversation", Messages: s.msgs})
}

// Budget is the latest metered budget (for the status bar).
func (s *Session) Budget() contextbudget.Budget { return s.budget }

// Run opens a session for dir and runs the interactive tview app until the user quits.
func Run(be backend.Backend, st *store.Store, dir string) error {
	sess, err := Open(be, st, dir)
	if err != nil {
		return err
	}
	model, nctx := "unknown model", 0
	if props, perr := be.Props(context.Background()); perr == nil {
		model, nctx = modelName(props.ModelID), props.NCtx
	}

	app := tview.NewApplication()
	transcript := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	status := tview.NewTextView().SetDynamicColors(true)
	input := tview.NewInputField().SetLabel("› ")

	setStatus := func() {
		b := sess.Budget()
		status.Clear()
		fmt.Fprintf(status, "[::d]%s · %s · ctx %d/%d[-]", sess.title, model, b.Used(), nctx)
	}
	setStatus()

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
		busy = true
		fmt.Fprintf(transcript, "\n[blue::b]you[-:-:-]  %s\n[green::b]llama[-:-:-]  ", tview.Escape(text))
		transcript.ScrollToEnd()
		go func() {
			rerr := sess.Turn(context.Background(), text, func(d backend.Delta) error {
				if d.Content != "" {
					app.QueueUpdateDraw(func() {
						fmt.Fprint(transcript, tview.Escape(d.Content))
						transcript.ScrollToEnd()
					})
				}
				return nil
			})
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
		if ev.Key() == tcell.KeyCtrlC {
			app.Stop()
			return nil
		}
		return ev
	})

	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(transcript, 0, 1, false).
		AddItem(input, 1, 0, true).
		AddItem(status, 1, 0, false)

	fmt.Fprintf(transcript, "[::d]llaminGO · %s · %s — type a message, Ctrl+C to quit[-]\n", sess.title, model)
	return app.SetRoot(flex, true).SetFocus(input).Run()
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

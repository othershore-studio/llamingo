// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/store"
)

// keepRecent is how many of the most recent messages compaction leaves verbatim.
const keepRecent = 4

const compactSystemPrompt = "You are compacting a chat conversation to save context. " +
	"Summarize the exchange below concisely, preserving key facts, decisions, names, and any " +
	"open threads or todos. Write only the summary."

// compactResult reports the outcome of a compaction: how many messages were
// folded, the net tokens saved, and the summary text produced.
type compactResult struct {
	Folded  int
	Saved   int
	Summary string
}

// shouldAutoCompact reports whether the projected prompt is full enough to fold
// the oldest messages before this turn.
func shouldAutoCompact(bud contextbudget.Budget) bool {
	return bud.Level() == contextbudget.LevelFull
}

// compactConversation folds a conversation's oldest messages into a summary and
// appends it (non-destructive). It returns a zero result (Folded 0) when there
// is nothing old enough to fold. Both the manual endpoint and auto-compaction
// use it.
func (s *Server) compactConversation(ctx context.Context, id string) (compactResult, error) {
	entries, err := s.st.LoadConversation(id)
	if err != nil {
		return compactResult{}, err
	}
	prior, fold, from, to := compactionPlan(entries)
	if len(fold) == 0 {
		return compactResult{}, nil
	}
	span := renderSpan(prior, fold)
	summary, err := summarize(ctx, s.be, span)
	if err != nil {
		return compactResult{}, err
	}
	replaced, _ := s.be.Tokenize(ctx, span)
	summaryTokens, _ := s.be.Tokenize(ctx, summary)
	model := ""
	if p, err := s.be.Props(ctx); err == nil {
		model = p.ModelID
	}
	if _, err := s.st.AppendSummary(id, store.SummaryInput{
		CoversFrom: from, CoversTo: to, Model: model, Content: summary,
		Replaced: replaced, SummaryTokens: summaryTokens,
	}); err != nil {
		return compactResult{}, err
	}
	saved := replaced - summaryTokens
	if saved < 0 {
		saved = 0
	}
	return compactResult{Folded: len(fold), Saved: saved, Summary: summary}, nil
}

// POST /api/compact {id} — fold the oldest messages of a conversation into a
// summary on demand (non-destructive). Returns {folded, saved, summary}.
func (s *Server) handleCompact(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	res, err := s.compactConversation(r.Context(), body.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"folded": res.Folded, "saved": res.Saved, "summary": res.Summary})
}

// compactionPlan decides what to fold: the messages after the current active
// summary, minus the most recent keepRecent kept verbatim. It returns the prior
// active summary (nil if none; it is re-summarized into the new one), the
// message entries to fold, and the id-range the new summary covers (the very
// first message through the last folded one).
func compactionPlan(entries []store.Entry) (prior *store.Entry, fold []store.Entry, from, to string) {
	var msgs []store.Entry
	for _, e := range entries {
		if e.Kind == store.KindMessage {
			msgs = append(msgs, e)
		}
	}
	if len(msgs) == 0 {
		return nil, nil, "", ""
	}
	prior, covered := store.ActiveSummary(entries)
	uncovered := msgs[covered:]
	if len(uncovered) <= keepRecent {
		return prior, nil, "", ""
	}
	fold = uncovered[:len(uncovered)-keepRecent]
	return prior, fold, msgs[0].ID, fold[len(fold)-1].ID
}

// renderSpan renders the prior summary (if any) and the folded messages into the
// text handed to the model for summarization.
func renderSpan(prior *store.Entry, fold []store.Entry) string {
	var b strings.Builder
	if prior != nil {
		b.WriteString("Summary of the conversation so far:\n")
		b.WriteString(prior.Content)
		b.WriteString("\n\n")
	}
	for i, m := range fold {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
	}
	return b.String()
}

// summarize asks the model for a compaction summary. It is a one-off internal
// call; the conversation's own budget is metered in handleChatStream.
func summarize(ctx context.Context, be backend.Backend, span string) (string, error) {
	var out strings.Builder
	err := be.ChatStream(ctx, backend.ChatRequest{Messages: []backend.Message{
		{Role: backend.RoleSystem, Content: compactSystemPrompt},
		{Role: backend.RoleUser, Content: span},
	}}, func(d backend.Delta) error {
		out.WriteString(d.Content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

// Package httpapi exposes the core over one HTTP + SSE API — the single surface
// the web UI consumes, in the Wails desktop app and (v2) the gateway PWA alike.
// Keeping every UI a thin client of this API is what makes the desktop shell
// reversible. See docs/architecture.md → "The core and its three faces".
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/store"
)

// Server is the HTTP handler for the core API.
type Server struct {
	be  backend.Backend
	st  *store.Store
	mux *http.ServeMux
}

// New wires the API over the given backend and store.
func New(be backend.Backend, st *store.Store) *Server {
	s := &Server{be: be, st: st, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/props", s.handleProps)
	s.mux.HandleFunc("GET /api/chats", s.handleListChats)
	s.mux.HandleFunc("POST /api/chats", s.handleNewChat)
	s.mux.HandleFunc("GET /api/projects", s.handleListProjects)
	s.mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	s.mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

// GET /api/props — the honest connect handshake.
func (s *Server) handleProps(w http.ResponseWriter, r *http.Request) {
	p, err := s.be.Props(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"model": p.ModelID, "nCtx": p.NCtx})
}

// GET /api/chats — the free chats, newest first.
func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	chats, err := s.st.ListChats()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if chats == nil {
		chats = []store.ConversationMeta{}
	}
	writeJSON(w, chats)
}

// POST /api/chats {title} — create a free chat.
func (s *Server) handleNewChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m, err := s.st.NewChat(body.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, m)
}

// GET /api/projects — the known projects, newest first.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	ps, err := s.st.ListProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ps == nil {
		ps = []store.ProjectMeta{}
	}
	writeJSON(w, ps)
}

// GET /api/conversations/{id} — the conversation's entries in log order, for
// the UI to render when a chat is opened.
func (s *Server) handleGetConversation(w http.ResponseWriter, r *http.Request) {
	entries, err := s.st.LoadConversation(r.PathValue("id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if entries == nil {
		entries = []store.Entry{}
	}
	writeJSON(w, entries)
}

// POST /api/chat/stream {id, content} — the core is the source of truth for
// history: it loads the conversation's log, appends this turn's user message,
// streams the reply as SSE ("delta" per chunk, then "done" with the honest
// budget), and appends the assistant reply. The client sends only the new turn.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	prior, err := s.st.LoadConversation(body.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if _, err := s.st.AppendMessage(body.ID, "user", body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	messages := append(messagesFromEntries(prior), backend.Message{Role: backend.RoleUser, Content: body.Content})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	bld := contextbudget.New(s.be)
	bud, err := bld.Estimate(r.Context(), []contextbudget.Segment{{Name: "conversation", Messages: messages}})
	if err != nil {
		writeEvent(w, flusher, "error", map[string]any{"error": err.Error()})
		return
	}

	var reply strings.Builder
	streamErr := s.be.ChatStream(r.Context(), backend.ChatRequest{Messages: messages}, func(d backend.Delta) error {
		if d.Content != "" {
			reply.WriteString(d.Content)
			writeEvent(w, flusher, "delta", map[string]any{"content": d.Content})
		}
		if d.Usage != nil {
			bud.Authoritative = d.Usage.PromptTokens
		}
		return nil
	})
	if streamErr != nil {
		writeEvent(w, flusher, "error", map[string]any{"error": streamErr.Error()})
		return
	}
	if _, err := s.st.AppendMessage(body.ID, "assistant", reply.String()); err != nil {
		writeEvent(w, flusher, "error", map[string]any{"error": err.Error()})
		return
	}
	writeEvent(w, flusher, "done", map[string]any{
		"budget": map[string]any{"used": bud.Used(), "nCtx": bud.NCtx, "level": string(bud.Level())},
	})
}

// messagesFromEntries projects a conversation's log to the message list sent to
// the model. Message entries map 1:1; summary entries (compaction, later) will
// be substituted by the context builder, so they are skipped here for now.
func messagesFromEntries(entries []store.Entry) []backend.Message {
	msgs := make([]backend.Message, 0, len(entries))
	for _, e := range entries {
		if e.Kind != store.KindMessage {
			continue
		}
		msgs = append(msgs, backend.Message{Role: backend.Role(e.Role), Content: e.Content})
	}
	return msgs
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, event string, data any) {
	b, _ := json.Marshal(data)
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
	flusher.Flush()
}

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
	"sync"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/contextbudget"
	"github.com/othershore-studio/llamingo/internal/debuglog"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
)

// Server is the HTTP handler for the core API.
type Server struct {
	be  backend.Backend
	st  *store.Store
	mux *http.ServeMux

	// Agentic wiring: the tool loop's permission gate reaches the user over the
	// chat stream. The broker + prompter are app-wide; gates are per-conversation
	// so session grants don't leak across chats (see permission.go).
	broker   *broker
	prompter *ssePrompter
	grants   permission.GrantStore
	gates    map[string]*permission.Gate
	gatesMu  sync.Mutex

	// Global MCP servers are connected once (lazily) and reused across chats.
	mcpOnce   sync.Once
	mcpGlobal mcp.Registry
}

// New wires the API over the given backend and store.
func New(be backend.Backend, st *store.Store) *Server {
	br := newBroker()
	s := &Server{
		be:       be,
		st:       st,
		mux:      http.NewServeMux(),
		broker:   br,
		prompter: newSSEPrompter(br),
		grants:   permission.NewMemoryGrantStore(),
		gates:    map[string]*permission.Gate{},
	}
	s.mux.HandleFunc("GET /api/props", s.handleProps)
	s.mux.HandleFunc("GET /api/chats", s.handleListChats)
	s.mux.HandleFunc("POST /api/chats", s.handleNewChat)
	s.mux.HandleFunc("GET /api/projects", s.handleListProjects)
	s.mux.HandleFunc("POST /api/projects", s.handleNewProject)
	s.mux.HandleFunc("GET /api/projects/{id}/sessions", s.handleListSessions)
	s.mux.HandleFunc("POST /api/projects/{id}/sessions", s.handleNewSession)
	s.mux.HandleFunc("POST /api/compact", s.handleCompact)
	s.mux.HandleFunc("GET /api/memory", s.handleGetMemory)
	s.mux.HandleFunc("PUT /api/memory", s.handlePutMemory)
	s.mux.HandleFunc("POST /api/memory/ack", s.handleAckMemory)
	s.mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	s.mux.HandleFunc("PUT /api/conversations/{id}", s.handleRenameConversation)
	s.mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	s.mux.HandleFunc("GET /api/conversations/{id}/attachments/{name}", s.handleGetAttachment)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	s.mux.HandleFunc("GET /api/imagegen", s.handleGetImagegen)
	s.mux.HandleFunc("PUT /api/imagegen", s.handlePutImagegen)
	s.mux.HandleFunc("POST /api/imagegen/fetch", s.handleFetchImagegen)
	s.mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	s.mux.HandleFunc("POST /api/permission/{id}", s.handlePermission)
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
	writeJSON(w, map[string]any{"model": modelDisplayName(p.ModelID, p.ModelAlias), "nCtx": p.NCtx, "thinkingSupported": thinkingSupported(p.ChatTemplate)})
}

// thinkingSupported reports whether the model's chat template exposes an
// enable_thinking variable — i.e. whether a per-request thinking toggle actually
// does anything. A template-string check (data /props already returns); a
// render-diff probe against /apply-template is a stronger v2 upgrade.
func thinkingSupported(chatTemplate string) bool {
	return strings.Contains(chatTemplate, "enable_thinking")
}

// modelDisplayName prefers llama.cpp's --alias as a friendly name, falling back
// to the model file's base name. The alias is ignored when it just echoes the
// path (llama.cpp's default when --alias is unset) or is itself path-like.
func modelDisplayName(path, alias string) string {
	a := strings.TrimSpace(alias)
	if a != "" && a != path && a != baseModelName(path) && !strings.ContainsAny(a, "/\\") && !strings.HasSuffix(a, ".gguf") {
		return a
	}
	return baseModelName(path)
}

// baseModelName is the model file's name without its directory or .gguf suffix.
func baseModelName(path string) string {
	if i := strings.LastIndexAny(path, "/\\"); i >= 0 {
		path = path[i+1:]
	}
	return strings.TrimSuffix(path, ".gguf")
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

// PUT /api/conversations/{id} {title} — rename a chat or session.
func (s *Server) handleRenameConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if err := s.st.Rename(r.PathValue("id"), title); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DELETE /api/conversations/{id} — delete a chat or session.
func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	if err := s.st.Delete(r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/settings — the llama-server connection (url + token), the user's
// saved location, and the Forge image-server URL.
func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	cfg, _ := s.st.Config()
	writeJSON(w, map[string]any{"url": rc.BaseURL(), "token": rc.Token(), "location": cfg.Location, "forgeUrl": cfg.ForgeURL, "debug": cfg.Debug})
}

// PUT /api/settings {url, token, location, forgeUrl, debug} — persist and hot-swap
// the connection (the next /props poll reconnects) and save location, Forge URL, debug.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	rc, ok := s.be.(backend.Reconfigurable)
	if !ok {
		http.Error(w, "backend is not reconfigurable", http.StatusNotImplemented)
		return
	}
	var body struct {
		URL      string `json:"url"`
		Token    string `json:"token"`
		Location string `json:"location"`
		ForgeURL string `json:"forgeUrl"`
		Debug    bool   `json:"debug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	url := strings.TrimSpace(body.URL)
	if url == "" {
		http.Error(w, "url is required", http.StatusBadRequest)
		return
	}
	if err := s.st.SetConfig(store.Config{URL: url, Token: body.Token, Location: strings.TrimSpace(body.Location), ForgeURL: strings.TrimSpace(body.ForgeURL), Debug: body.Debug}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rc.SetConnection(url, body.Token)
	w.WriteHeader(http.StatusNoContent)
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

// POST /api/projects {path, title} — open a working folder as a project.
// Idempotent: re-opening the same path returns the existing project.
func (s *Server) handleNewProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.Path) == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	m, err := s.st.OpenProject(body.Path, body.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, m)
}

// GET /api/projects/{id}/sessions — a project's sessions, newest first.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.st.ListSessions(store.ProjectID(r.PathValue("id")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if sessions == nil {
		sessions = []store.ConversationMeta{}
	}
	writeJSON(w, sessions)
}

// POST /api/projects/{id}/sessions {title} — create a session in a project.
func (s *Server) handleNewSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m, err := s.st.NewSession(store.ProjectID(r.PathValue("id")), body.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, m)
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
// streams the reply as SSE ("reasoning" and "delta" chunks, then "done" with
// the honest budget), and appends the assistant reply. The client sends only
// the new turn. Reasoning ("thinking") streams live and is persisted as a
// reasoning entry (shown when a chat is reopened) but kept out of the prompt —
// see store.AppendAssistant and store.Project.
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID       string `json:"id"`
		Content  string `json:"content"`
		Thinking *bool  `json:"thinking"` // nil → leave the server default; set → chat_template_kwargs.enable_thinking
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
	messages := append(messagesFromEntries(store.Project(prior)), backend.Message{Role: backend.RoleUser, Content: body.Content})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	bld := contextbudget.New(s.be)
	segs := s.segmentsFor(messages)
	bud, err := bld.Estimate(r.Context(), segs)
	if err != nil {
		writeEvent(w, flusher, "error", map[string]any{"error": err.Error()})
		return
	}

	// Auto-compact before the turn when the projected prompt is full, then
	// rebuild the prompt on the folded log and re-meter.
	if shouldAutoCompact(bud) {
		if res, cerr := s.compactConversation(r.Context(), body.ID); cerr == nil && res.Folded > 0 {
			if entries, lerr := s.st.LoadConversation(body.ID); lerr == nil {
				messages = messagesFromEntries(store.Project(entries))
				segs = s.segmentsFor(messages)
				if b, eerr := bld.Estimate(r.Context(), segs); eerr == nil {
					bud = b
				}
				writeEvent(w, flusher, "compacted", map[string]any{"folded": res.Folded, "saved": res.Saved, "summary": res.Summary})
			}
		}
	}

	// Drive the client-owned tool loop instead of a bare completion: the model
	// may call tools (write_memory now; image gen / MCP servers later), each
	// mediated by this conversation's permission gate. The emitter in the context
	// lets the gate surface a consent prompt on this very SSE stream, and the
	// answer returns on POST /api/permission/{id} (see permission.go).
	emit := func(event string, data any) { writeEvent(w, flusher, event, data) }
	ctx := withEmitter(r.Context(), emit)
	// Debug mode: tee this turn's raw llama/Forge/tool payloads into the chat's
	// own logs/ folder (see internal/debuglog). Off unless the setting is on.
	if cfg, _ := s.st.Config(); cfg.Debug {
		if dir, derr := s.st.LogDir(body.ID); derr == nil {
			ctx = debuglog.With(ctx, &debuglog.Sink{Dir: dir})
		}
	}
	reg := s.registryFor(body.ID)

	// The thinking toggle maps to the model's chat template: when the client sets
	// it, pass enable_thinking so the user controls reasoning per turn rather than
	// inheriting the server default. Omitted when unset.
	var kwargs map[string]any
	if body.Thinking != nil {
		kwargs = map[string]any{"enable_thinking": *body.Thinking}
	}

	var think strings.Builder
	var timings *backend.Timings
	out, runErr := mcp.Run(ctx, s.be, reg, bld.Messages(segs), func(d backend.Delta) error {
		if d.Reasoning != "" {
			think.WriteString(d.Reasoning)
			emit("reasoning", map[string]any{"content": d.Reasoning})
		}
		if d.Content != "" {
			emit("delta", map[string]any{"content": d.Content})
		}
		if d.Done {
			for _, tc := range d.ToolCalls {
				emit("tool_call", map[string]any{"name": tc.Function.Name, "arguments": tc.Function.Arguments})
			}
		}
		if d.Usage != nil && d.Usage.PromptTokens > bud.Authoritative {
			bud.Authoritative = d.Usage.PromptTokens
		}
		if d.Timings != nil {
			timings = d.Timings
		}
		return nil
	}, maxToolRounds, kwargs)
	if runErr != nil {
		emit("error", map[string]any{"error": runErr.Error()})
		return
	}
	// Assemble the saved reply as a layered, deterministic result. The model is
	// steered during the post-tool "shaping" round (mcp.shapingGuidance) to write
	// a clean caption and not repeat the image; here we (1) strip any ReAct scratch
	// blob it emitted anyway, then (2) attach the image deterministically from the
	// tool result, so the picture always shows in a consistent place. Finally,
	// persist any linked image and rewrite it to a local ref. See toolblob.go and
	// attachment.go.
	answer := recoverToolImages(stripLeadingToolBlob(finalAssistant(out)), out)
	answer = surfaceImageDenial(answer, out) // a declined/timed-out image isn't left as a blank reply
	answer = s.imageAttachments(r.Context(), body.ID, answer)
	// Persist the turn's trail so it survives a reopen: the thinking, the tools it
	// used, then the answer. The trail entries are display-only (Project drops them,
	// like reasoning), so they never re-enter the model's prompt — only the answer
	// does. See store.AppendReasoning / AppendToolUse / Project.
	_ = s.st.AppendReasoning(body.ID, think.String())
	for _, tc := range toolCallsFrom(out) {
		_ = s.st.AppendToolUse(body.ID, tc.name, tc.args)
	}
	if _, err := s.st.AppendMessage(body.ID, "assistant", answer); err != nil {
		emit("error", map[string]any{"error": err.Error()})
		return
	}
	donePayload := map[string]any{
		"budget": map[string]any{"used": bud.Used(), "nCtx": bud.NCtx, "level": string(bud.Level())},
	}
	if timings != nil {
		donePayload["timings"] = map[string]any{
			"promptPerSecond":  timings.PromptPerSecond,
			"predictPerSecond": timings.PredictPerSecond,
			"predictN":         timings.PredictN,
		}
	}
	emit("done", donePayload)
}

// messagesFromEntries maps projected log entries to the message list sent to the
// model: a message entry keeps its role; a summary entry (produced by compaction
// and substituted for its covered range by store.Project) becomes a system
// message carrying the folded context.
func messagesFromEntries(entries []store.Entry) []backend.Message {
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

// maxToolRounds caps the client-owned tool loop so a misbehaving model cannot
// spin forever; 8 rounds is ample for the native tools and typical MCP use.
const maxToolRounds = 8

// finalAssistant returns the content of the loop's last assistant message — the
// answer after any tools have settled. Intermediate tool-call turns and tool
// results are not persisted (model scratch, like reasoning): the live SSE stream
// shows them in the moment, and a tool's durable effect is its own artifact
// (e.g. the memory file). Persisting the tool trail is a later log-schema change.
func finalAssistant(msgs []backend.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == backend.RoleAssistant {
			return msgs[i].Content
		}
	}
	return ""
}

// toolCall is one tool the model invoked this turn (name + raw args), for the
// persisted display trail.
type toolCall struct{ name, args string }

// toolCallsFrom returns the tools the model called across the turn's rounds, in
// order — the trail persisted (display-only) alongside the reasoning.
func toolCallsFrom(msgs []backend.Message) []toolCall {
	var calls []toolCall
	for _, m := range msgs {
		if m.Role == backend.RoleAssistant {
			for _, tc := range m.ToolCalls {
				calls = append(calls, toolCall{name: tc.Function.Name, args: tc.Function.Arguments})
			}
		}
	}
	return calls
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

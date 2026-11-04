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
	"github.com/othershore-studio/llamingo/internal/toolreply"
)

// Server is the HTTP handler for the core API.
type Server struct {
	be      backend.Backend
	st      *store.Store
	cfg     *store.Store // SERVER config (connections/Forge/MCP/settings); == st unless WithConfigStore — taita shares one central config store across its per-user cores
	mux     *http.ServeMux
	mode    string     // "local" (a client sidecar) or "server" (taita); see GET /api/mode
	taitaMu sync.Mutex // guards taita (relinkable at runtime via Settings)
	taita   *taitaLink // set → this local sidecar proxies a linked taita (serverproxy.go)

	// Agentic wiring: the tool loop's permission gate reaches the user over the
	// chat stream. The broker + prompter are app-wide; gates are per-conversation
	// so session grants don't leak across chats (see permission.go).
	broker   *broker
	prompter *ssePrompter
	grants   permission.GrantStore
	gates    map[string]*permission.Gate
	gatesMu  sync.Mutex

	// Global MCP servers are connected lazily and reused across chats; the registry
	// is rebuilt in place when the MCP config changes, so edits apply without an
	// app restart (see reloadGlobalServers).
	mcpMu     sync.Mutex
	mcpGlobal mcp.Registry
	// Per-project MCP servers (from each project's own .mcp.json), keyed by the
	// project's working folder and invalidated by the file's mtime — so a project's
	// servers connect per project, and an edit applies without a restart.
	projectRegs map[string]*projectReg

	// Per-conversation set of files read via read_file, so write_file can enforce
	// read-before-overwrite within a chat (a new file is exempt). In-memory only.
	fileReadsMu sync.Mutex
	fileReads   map[string]map[string]bool
}

// Option configures a Server at construction; without any, it runs in "local"
// mode (a client's sidecar). taita passes ServerMode() for its per-user cores.
type Option func(*Server)

// ServerMode marks this core as a taita server rather than a local client's
// sidecar, surfaced at GET /api/mode so the shared web UI adapts (server =
// chat-only, no Projects, one server-owned chat list). See the memory note
// taita-build-plan → TWO CHAT LISTS.
func ServerMode() Option { return func(s *Server) { s.mode = "server" } }

// WithConfigStore points this core's SERVER CONFIG (connections, Forge, MCP,
// settings) at a store separate from its data (chat) store. taita passes one
// shared config store to every per-user core, so the whole server has a single,
// central, admin-owned configuration; without it, config lives in the data store
// — exactly today's single-user behavior. See docs/taita.md (config/data split).
func WithConfigStore(cs *store.Store) Option { return func(s *Server) { s.cfg = cs } }

// New wires the API over the given backend and store. Without options it is a
// local client sidecar; pass ServerMode() for a taita core.
func New(be backend.Backend, st *store.Store, opts ...Option) *Server {
	br := newBroker()
	s := &Server{
		be:          be,
		st:          st,
		cfg:         st,
		mode:        "local",
		mux:         http.NewServeMux(),
		broker:      br,
		prompter:    newSSEPrompter(br),
		grants:      permission.NewMemoryGrantStore(),
		gates:       map[string]*permission.Gate{},
		projectRegs: map[string]*projectReg{},
		fileReads:   map[string]map[string]bool{},
	}
	for _, opt := range opts {
		opt(s)
	}
	// A linked taita persists in config (set via Settings); adopt it at startup,
	// unless an option already linked one (e.g. tests).
	if !s.taitaLinked() {
		if cfg, cerr := s.cfg.Config(); cerr == nil {
			s.setTaita(cfg.TaitaURL)
		}
	}
	s.mux.HandleFunc("GET /api/props", s.handleProps)
	s.mux.HandleFunc("GET /api/mode", s.handleMode)
	s.mux.HandleFunc("GET /api/server/chats", s.handleServerChats)
	s.mux.HandleFunc("POST /api/server/chats", s.handleServerChats)
	s.mux.HandleFunc("GET /api/server/conversations/{id}", s.handleServerConversation)
	s.mux.HandleFunc("GET /api/server/conversations/{id}/budget", s.handleServerBudget)
	s.mux.HandleFunc("POST /api/server/chat/stream", s.handleServerChatStream)
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
	s.mux.HandleFunc("GET /api/conversations/{id}/budget", s.handleGetBudget)
	s.mux.HandleFunc("PUT /api/conversations/{id}", s.handleRenameConversation)
	s.mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	s.mux.HandleFunc("GET /api/conversations/{id}/attachments/{name}", s.handleGetAttachment)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	s.mux.HandleFunc("GET /api/prefs", s.handleGetPrefs)
	s.mux.HandleFunc("PUT /api/prefs", s.handlePutPrefs)
	s.mux.HandleFunc("GET /api/autostart", s.handleGetAutostart)
	s.mux.HandleFunc("PUT /api/autostart", s.handlePutAutostart)
	s.mux.HandleFunc("GET /api/imagegen", s.handleGetImagegen)
	s.mux.HandleFunc("PUT /api/imagegen", s.handlePutImagegen)
	s.mux.HandleFunc("POST /api/imagegen/fetch", s.handleFetchImagegen)
	s.mux.HandleFunc("GET /api/conversations/{id}/tools", s.handleGetConversationTools)
	s.mux.HandleFunc("PUT /api/conversations/{id}/tools", s.handlePutConversationTools)
	s.mux.HandleFunc("GET /api/mcp", s.handleGetMCP)
	s.mux.HandleFunc("PUT /api/mcp", s.handlePutMCP)
	s.mux.HandleFunc("GET /api/project", s.handleGetProject)
	s.mux.HandleFunc("GET /api/project/status", s.handleGetProjectStatus)
	s.mux.HandleFunc("PUT /api/project", s.handlePutProject)
	s.mux.HandleFunc("GET /api/connections", s.handleGetConnections)
	s.mux.HandleFunc("PUT /api/connections", s.handlePutConnections)
	s.mux.HandleFunc("POST /api/connections/activate", s.handleActivateConnection)
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

// GET /api/mode — whether this core is a local client sidecar ("local") or a
// taita server ("server"). Backend-independent (unlike /api/props), so the shared
// web UI can pick its layout even when the model is down. See taita-build-plan.
func (s *Server) handleMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"mode": s.mode, "serverLinked": s.taitaLinked()})
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
	cfg, _ := s.cfg.Config()
	writeJSON(w, map[string]any{"url": rc.BaseURL(), "token": rc.Token(), "location": cfg.Location, "forgeUrl": cfg.ForgeURL, "taitaUrl": cfg.TaitaURL, "debug": cfg.Debug})
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
		TaitaURL string `json:"taitaUrl"`
		Debug    bool   `json:"debug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Read-modify-write so any fields this form does not own survive a settings save.
	cfg, _ := s.cfg.Config()
	// The connection now lives in its own card (/api/connections); a Settings save
	// that omits the URL leaves the active connection untouched and updates only the
	// fields below. A URL is still honored (and hot-swapped) for back-compat.
	url := strings.TrimSpace(body.URL)
	if url != "" {
		cfg.URL = url
		cfg.Token = body.Token
	}
	cfg.Location = strings.TrimSpace(body.Location)
	cfg.ForgeURL = strings.TrimSpace(body.ForgeURL)
	cfg.TaitaURL = strings.TrimSpace(body.TaitaURL)
	cfg.Debug = body.Debug
	if err := s.cfg.SetConfig(cfg); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if url != "" {
		rc.SetConnection(url, body.Token)
	}
	s.setTaita(cfg.TaitaURL) // relink the client's Server Chats (empty = unlink)
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

// GET /api/conversations/{id}/budget — the honest context budget for the chat's
// CURRENT state, with no new turn: memory + the projected history, tokenized by
// the backend. This is what lets the status bar show usage before the first
// message and again when a chat is reopened, not only after a turn's `done`. It
// mirrors handleChatStream's pre-turn estimate, so (as there) tool schemas aren't
// counted — the authoritative correction only lands once a turn actually runs.
func (s *Server) handleGetBudget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// ?fresh=1 forces a re-meter (e.g. the chat's active tools just changed),
	// ignoring the persisted last-turn count. Otherwise a chat that has had a turn
	// shows that authoritative, tool-inclusive count straight from meta — no
	// re-metering, no MCP connection.
	if r.URL.Query().Get("fresh") == "" {
		if b, ok, err := s.st.ConversationBudget(id); err == nil && ok {
			writeJSON(w, map[string]any{"used": b.Used, "nCtx": b.NCtx, "level": b.Level})
			return
		}
	}
	// A fresh chat has no turn yet: meter the baseline it would start from —
	// memory + the local system tools (no MCP, so opening connects nothing) + the
	// (empty) history — so usage shows before the first message, tools included.
	prior, err := s.st.LoadConversation(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	messages := messagesFromEntries(store.Project(prior))
	bud, err := contextbudget.New(s.be).Estimate(r.Context(), s.segmentsFor(id, messages, s.budgetTools(id)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]any{"used": bud.Used(), "nCtx": bud.NCtx, "level": string(bud.Level())})
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
	// Give a fresh conversation a tentative name from its first message (no-op once
	// named). Best-effort — a title hiccup must not fail the turn.
	_ = s.st.MaybeAutoTitle(body.ID, body.Content)
	messages := append(messagesFromEntries(store.Project(prior)), backend.Message{Role: backend.RoleUser, Content: body.Content})

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	bld := contextbudget.New(s.be)
	segs := s.segmentsFor(body.ID, messages, nil)
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
				segs = s.segmentsFor(body.ID, messages, nil)
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
	if cfg, _ := s.cfg.Config(); cfg.Debug {
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
	answer := toolreply.RecoverToolImages(toolreply.StripLeadingToolBlob(finalAssistant(out)), out)
	answer = toolreply.SurfaceImageDenial(answer, out) // a declined/timed-out image isn't left as a blank reply
	answer = s.imageAttachments(r.Context(), body.ID, answer)
	// Persist the turn's trail so it survives a reopen: the thinking, the tools it
	// used, then the answer. The trail entries are display-only (Project drops them,
	// like reasoning), so they never re-enter the model's prompt — only the answer
	// does. See store.AppendReasoning / AppendToolUse / Project.
	_ = s.st.AppendReasoning(body.ID, think.String())
	for _, tc := range toolCallsFrom(out) {
		_ = s.st.AppendToolUse(body.ID, tc.name, tc.args)
	}
	tps := 0.0
	if timings != nil {
		tps = timings.PredictPerSecond
	}
	if _, err := s.st.AppendMessageTimed(body.ID, "assistant", answer, tps); err != nil {
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
	// Persist this turn's accounting so the status bar shows it again on reopen,
	// without re-metering (and with the authoritative count folded in). See
	// handleGetBudget.
	_ = s.st.SetConversationBudget(body.ID, store.BudgetStatus{Used: bud.Used(), NCtx: bud.NCtx, Level: string(bud.Level())})
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

// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/othershore-studio/llamingo/internal/backend"
	"github.com/othershore-studio/llamingo/internal/filetools"
	"github.com/othershore-studio/llamingo/internal/imagegen"
	"github.com/othershore-studio/llamingo/internal/localtools"
	"github.com/othershore-studio/llamingo/internal/mcp"
	"github.com/othershore-studio/llamingo/internal/memtool"
	"github.com/othershore-studio/llamingo/internal/permission"
	"github.com/othershore-studio/llamingo/internal/store"
)

// This file is the agentic wiring: the permission gate reaches the user over the
// one-way SSE chat stream. The gate's Prompter is called deep inside the tool
// loop, on the handler goroutine, with the request context in hand; it emits a
// "permission" event on that stream and blocks. The answer returns out-of-band
// on POST /api/permission/{id}, which the broker routes back to the waiting
// call. See docs/architecture.md → "The permission handshake".

// permissionTimeout is how long a tool waits for the user to answer before it is
// auto-denied (the safe default). A dropped connection is reaped sooner by the
// request context.
const permissionTimeout = 2 * time.Minute

// emitter writes an SSE event on the in-flight chat stream. handleChatStream
// stashes one in the request context so the gate's prompter — reached deep in
// the tool loop — can surface a consent prompt on the very stream that provoked
// it.
type emitter func(event string, data any)

type emitterKeyT struct{}

var emitterKey emitterKeyT

func withEmitter(ctx context.Context, e emitter) context.Context {
	return context.WithValue(ctx, emitterKey, e)
}

func emitterFrom(ctx context.Context) emitter {
	e, _ := ctx.Value(emitterKey).(emitter)
	return e
}

// broker correlates a consent prompt (sent out over the SSE stream) with its
// answer (arriving on a separate POST). Each pending prompt gets an id and a
// buffered channel; the POST handler resolves it, unblocking the waiting Ask.
type broker struct {
	mu      sync.Mutex
	pending map[string]chan permission.Decision
}

func newBroker() *broker { return &broker{pending: map[string]chan permission.Decision{}} }

func (b *broker) register(id string) chan permission.Decision {
	ch := make(chan permission.Decision, 1)
	b.mu.Lock()
	b.pending[id] = ch
	b.mu.Unlock()
	return ch
}

func (b *broker) cancel(id string) {
	b.mu.Lock()
	delete(b.pending, id)
	b.mu.Unlock()
}

// resolve delivers a decision to the waiting Ask. It reports whether a prompt
// with that id was pending (false → unknown, or already answered/expired). The
// channel is buffered, so the send never blocks the POST handler.
func (b *broker) resolve(id string, dec permission.Decision) bool {
	b.mu.Lock()
	ch, ok := b.pending[id]
	b.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- dec:
		return true
	default:
		return false // already answered
	}
}

// ssePrompter is the production permission.Prompter: it surfaces a consent
// prompt on the request's SSE stream and blocks until the user answers via POST,
// the request is cancelled, or the timeout elapses (auto-deny). It is app-scoped
// and shared by every per-conversation gate; the per-request stream is found in
// the context, so one prompter serves all in-flight chats.
type ssePrompter struct {
	broker  *broker
	timeout time.Duration
}

func newSSEPrompter(b *broker) *ssePrompter {
	return &ssePrompter{broker: b, timeout: permissionTimeout}
}

// Ask implements permission.Prompter.
func (p *ssePrompter) Ask(ctx context.Context, req permission.Request, allowed []permission.Scope) (permission.Decision, error) {
	emit := emitterFrom(ctx)
	log.Printf("permission: prompt %q (tool %s, stream=%t)", req.Label, req.ToolID, emit != nil)
	if emit == nil {
		// No stream to ask on — deny rather than act unasked.
		return permission.Decision{}, nil
	}
	id, err := newRequestID()
	if err != nil {
		return permission.Decision{}, err
	}
	ch := p.broker.register(id)
	defer p.broker.cancel(id)

	emit("permission", map[string]any{
		"id":     id,
		"label":  req.Label,
		"detail": req.Detail,
		"risk":   riskName(req.Risk),
		"scopes": scopeNames(allowed),
	})

	select {
	case dec := <-ch:
		return dec, nil
	case <-ctx.Done():
		return permission.Decision{}, ctx.Err()
	case <-time.After(p.timeout):
		return permission.Decision{}, nil // auto-deny on silence
	}
}

// POST /api/permission/{id} {allow, scope} — the answer to a consent prompt,
// routed back to the tool call waiting on the chat stream.
func (s *Server) handlePermission(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Allow bool   `json:"allow"`
		Scope string `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dec := permission.Decision{Allow: body.Allow, Scope: parseScope(body.Scope)}
	if !s.broker.resolve(r.PathValue("id"), dec) {
		http.Error(w, "no such pending permission", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// gateFor returns the permission gate for a conversation, creating it on first
// use. Session grants ("allow for this chat") live in the gate's own memory, so
// a gate per conversation keeps them from leaking across chats; persistent
// grants and the prompter are shared app-wide.
func (s *Server) gateFor(convID string) *permission.Gate {
	s.gatesMu.Lock()
	defer s.gatesMu.Unlock()
	g, ok := s.gates[convID]
	if !ok {
		g = permission.New(s.prompter, s.grants)
		s.gates[convID] = g
	}
	return g
}

// registryFor builds the tool registry offered for a conversation's turn: the
// native write_memory and local ambient tools and the user's global MCP servers,
// plus the project file tools in a project session and the image tools when image
// generation is configured. write_memory and image generation are gated; the
// local and file tools are read-only.
func (s *Server) registryFor(convID string) mcp.Registry {
	gate := s.gateFor(convID)
	regs := []mcp.Registry{s.globalServers(), memtool.NewTool(s.st, gate), localtools.NewTool(s.locationSetting)}
	// The project file tools are confined to the session's project folder, so
	// they are offered only in a project session (a free chat has no root); the
	// project's own MCP servers (its .mcp.json) are offered there too.
	if root, ok, err := s.st.ProjectRoot(convID); err == nil && ok && root != "" {
		regs = append(regs, filetools.NewTool(root, gate, s.fileReadsFor(convID)))
		if pr := s.projectServers(root); pr != nil {
			regs = append(regs, pr)
		}
	}
	// Image generation, when a Forge URL and at least one model are configured.
	if tool := s.imageTool(convID, gate); tool != nil {
		regs = append(regs, tool)
	}
	// Curation: drop the tools turned off for THIS chat, so a small model isn't
	// drowned by the full set (per-chat, read fresh each turn — see mcp.Curate and
	// /api/conversations/{id}/tools).
	reg := mcp.Combine(regs...)
	if disabled, err := s.st.ConversationTools(convID); err == nil {
		reg = mcp.Curate(reg, toSet(disabled))
	}
	return reg
}

// fileReadsFor returns the per-conversation set of files read via read_file,
// creating it on first use. write_file consults it for read-before-overwrite.
func (s *Server) fileReadsFor(convID string) map[string]bool {
	s.fileReadsMu.Lock()
	defer s.fileReadsMu.Unlock()
	m := s.fileReads[convID]
	if m == nil {
		m = map[string]bool{}
		s.fileReads[convID] = m
	}
	return m
}

// budgetTools returns the tool definitions to METER for a conversation, curated
// per chat: the local system tools (memory, location, project files, image gen)
// plus the user's global MCP servers — whose schemas are a big chunk of the prompt
// (far larger than the system tools), so a chat with them enabled reads honestly
// and can warn when they overflow a small context. The global servers connect once
// and are reused (the same connect a turn does), so the budget counts them from
// the first open, not only after a turn. Project MCP is left out here (it would
// trip its per-project gate); its cost lands in the authoritative count once a
// turn runs and is then persisted (see SetConversationBudget).
func (s *Server) budgetTools(convID string) []backend.ToolDef {
	regs := []mcp.Registry{memtool.NewTool(s.st, nil), localtools.NewTool(s.locationSetting)}
	if g := s.globalServers(); g != nil {
		regs = append(regs, g)
	}
	if root, ok, err := s.st.ProjectRoot(convID); err == nil && ok && root != "" {
		regs = append(regs, filetools.NewTool(root, nil, nil))
	}
	if tool := s.imageTool(convID, nil); tool != nil {
		regs = append(regs, tool)
	}
	reg := mcp.Combine(regs...)
	if disabled, err := s.st.ConversationTools(convID); err == nil {
		reg = mcp.Curate(reg, toSet(disabled))
	}
	return reg.Tools()
}

// imageTool builds the image-generation tool for a conversation when a Forge URL
// (the setting or LLAMINGO_FORGE_URL) and at least one configured model are
// present, else nil. Generated images save into that conversation's attachments/
// — the same sink the persisted tool images use.
func (s *Server) imageTool(convID string, gate *permission.Gate) mcp.Registry {
	cfg, _ := s.cfg.Config()
	url := cfg.ForgeURL
	if url == "" {
		url = os.Getenv("LLAMINGO_FORGE_URL")
	}
	if url == "" {
		return nil
	}
	imgCfg, err := imagegen.LoadConfig(filepath.Join(s.cfg.Root(), "imagegen.json"))
	if err != nil {
		log.Printf("mcp: image config: %v", err)
		return nil
	}
	if len(imgCfg.Models) == 0 {
		return nil
	}
	return imagegen.NewTool(imagegen.NewForgeNeo(url), convSink{st: s.st, convID: convID}, gate, imgCfg)
}

// convSink saves image bytes into one conversation's attachments/ folder.
type convSink struct {
	st     *store.Store
	convID string
}

func (s convSink) Save(ctx context.Context, name string, data []byte) (string, error) {
	return s.st.SaveAttachment(s.convID, name, data)
}

// locationSetting returns the user's configured location ("" if unset or on a
// read error). The local get_location tool reads it through this.
func (s *Server) locationSetting() string {
	c, err := s.cfg.Config()
	if err != nil {
		return ""
	}
	return c.Location
}

// globalServers connects the user's global MCP servers once and caches the
// result. Global servers are user-authored, so they run trusted (no per-call
// prompt); a server that fails to start is logged and skipped rather than
// breaking chat.
func (s *Server) globalServers() mcp.Registry {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if s.mcpGlobal == nil {
		s.mcpGlobal = s.connectGlobalServers()
	}
	return s.mcpGlobal
}

// connectGlobalServers builds a registry of the user's global MCP servers, each
// connected; a failure is logged and skipped, not fatal. Global servers are
// user-authored and trusted, so the gate is unused here.
func (s *Server) connectGlobalServers() *mcp.ServerRegistry {
	reg := mcp.NewRegistry(nil)
	servers, err := mcp.Load(globalMCPPath(s.cfg.Root()), "")
	if err != nil {
		log.Printf("mcp: load global config: %v", err)
	}
	for _, srv := range servers {
		c, err := mcp.Connect(srv)
		if err != nil {
			log.Printf("mcp: connect %q: %v", srv.Name, err)
			continue
		}
		if err := reg.Add(context.Background(), srv, c); err != nil {
			log.Printf("mcp: add %q: %v", srv.Name, err)
			_ = c.Close()
			continue
		}
		log.Printf("mcp: connected global server %q", srv.Name)
	}
	return reg
}

// reloadGlobalServers rebuilds the global MCP registry from the current config —
// closing the old connections and reconnecting — so a config edit (add / edit /
// remove a server) applies on the next turn without an app restart.
func (s *Server) reloadGlobalServers() {
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if old, ok := s.mcpGlobal.(*mcp.ServerRegistry); ok {
		old.Close()
	}
	s.mcpGlobal = s.connectGlobalServers()
}

// projectReg is a project's connected MCP servers plus the mtime of the .mcp.json
// they came from, so a changed file reconnects (no restart).
type projectReg struct {
	reg   *mcp.ServerRegistry
	mtime time.Time
}

// projectMCPPath is a project's own MCP config, in its working folder — the
// standard .mcp.json location, shared with other tools that read it.
func projectMCPPath(root string) string { return filepath.Join(root, ".mcp.json") }

// projectServers returns the MCP servers declared in a project's .mcp.json,
// connected and cached per project (keyed by its folder, invalidated by the file's
// mtime so an edit — in the file or the UI — applies on the next turn, no restart).
// Project servers are not user-authored (ScopeProject), so their tools clear the
// permission gate on first use; the gate is per project, so consent is shared
// across that project's sessions. Returns nil when the project has no .mcp.json.
func (s *Server) projectServers(root string) mcp.Registry {
	fi, err := os.Stat(projectMCPPath(root))
	if err != nil {
		return nil
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	if pr, ok := s.projectRegs[root]; ok {
		if pr.mtime.Equal(fi.ModTime()) {
			return pr.reg
		}
		pr.reg.Close() // the file changed — reconnect
	}
	reg := s.connectProjectServers(root)
	s.projectRegs[root] = &projectReg{reg: reg, mtime: fi.ModTime()}
	return reg
}

// connectProjectServers connects the servers in a project's .mcp.json, each gated
// (project-scoped, not user-authored, so consent on first use).
func (s *Server) connectProjectServers(root string) *mcp.ServerRegistry {
	reg := mcp.NewRegistry(permission.New(s.prompter, s.grants))
	servers, err := mcp.Load("", projectMCPPath(root))
	if err != nil {
		log.Printf("mcp: load project config %q: %v", root, err)
	}
	for _, srv := range servers {
		c, err := mcp.Connect(srv)
		if err != nil {
			log.Printf("mcp: connect project %q: %v", srv.Name, err)
			continue
		}
		if err := reg.Add(context.Background(), srv, c); err != nil {
			log.Printf("mcp: add project %q: %v", srv.Name, err)
			_ = c.Close()
			continue
		}
		log.Printf("mcp: connected project server %q (%s)", srv.Name, root)
	}
	return reg
}

// globalMCPPath is the global MCP config in the store root — mcp.json, or a
// copied-in .mcp.json (Claude's per-project filename) as a convenience. With
// neither present it returns the canonical mcp.json, which Load treats as empty.
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

func newRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func riskName(r permission.Risk) string {
	switch r {
	case permission.RiskLow:
		return "low"
	case permission.RiskHigh:
		return "high"
	default:
		return "medium"
	}
}

func scopeNames(scopes []permission.Scope) []string {
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, scopeName(s))
	}
	return out
}

func scopeName(s permission.Scope) string {
	switch s {
	case permission.ScopeSession:
		return "session"
	case permission.ScopePersistent:
		return "persistent"
	default:
		return "once"
	}
}

func parseScope(name string) permission.Scope {
	switch name {
	case "session":
		return permission.ScopeSession
	case "persistent":
		return permission.ScopePersistent
	default:
		return permission.ScopeOnce
	}
}

# llaminGO — UX & Interface

How the client looks and behaves. The guiding sensibility is borrowed wholesale from **margin** (the author's
shipping notes app): *calm, non-blocking, never-lose-data, no-hidden-state* — restraint over options. llaminGO
re-implements that taste as a small web token set (the UI is web — see [`architecture.md`](architecture.md)), not
margin's Flutter code.

The structural adaptation in one line: margin is **tree + editor**; llaminGO is **conversation list + thread**,
because a chat client's "documents" are conversations, not files.

## The sensibility (what "calm" means here)

- **Never block.** The UI doesn't stop to ask; it surfaces passive cues and lets work continue.
- **Never lose data — or silently act on stale data.** Safety is forgiving and automatic.
- **No hidden state.** Cues are *derived from what's on disk* (the plain files), so they are durable and
  self-clearing — margin's conflict-marker-is-the-filename rule, applied to context.
- **One model adapted per platform**, not divergent designs: one responsive web UI, desktop ↔ mobile.
- Flat (no gradients/shadows), hairline borders, two type weights, sentence case, translucent selection.

## The shell

A **frameless, merged top bar** — the OS title bar is hidden and llaminGO draws one bar: app mark · history nav ·
`project ▸ session` breadcrumb · model + effort selector · overflow · its own window buttons (macOS keeps native
traffic-lights). Below it a **two-panel body** (left nav + conversation) that **collapses to conversation-only**. A
**status bar** across the bottom carries live state (connection · model · the context budget). Narrow/mobile:
conversation is primary and the left panel becomes a **slide-over drawer** (overlays, doesn't push). It is the same
web UI everywhere, so the v2 PWA is free (see [`architecture.md`](architecture.md) → *The core and its three faces*).

## The left panel — scope + sessions

- A **scope switcher** at the top selects the active context: **Chats** (the free root) or a **Project**. Switching
  swaps the list below to that scope's conversations.
- The switcher is **scope selection, not just navigation** — "Project X" means you are in X's `AGENTS.md` +
  `memory.md` + permission grants. It is the UI embodiment of the folder/chat scoping axis
  (see [`architecture.md`](architecture.md) → *Storage*).
- Below it, a **flat list** of the scope's conversations — sessions don't nest, so there is no tree. `+ New session` /
  `+ New chat` sits at the top; `Open folder…` in the switcher creates/anchors a project.
- **Chats is a peer scope**, reached from the same switcher and surfaced in the **global quick-switcher** (fuzzy-find
  across *all* scopes) — never a siloed tab you forget.
- **Files are not in the panel.** They enter a conversation **in-flow via `@`-mention** in the composer (project
  scope only). A persistent file view, if ever wanted, is a `Sessions ⟷ Files` toggle — never a third column.

## Sessions as context (the small-model strategy)

This is why the two-level model matters beyond organization:

- The **project is the durable, low-churn layer** — its files + `AGENTS.md` + `memory.md`.
- A **session is a clean, bounded context** for one task. Nothing from task A bleeds into task B; only the durable
  layer crosses sessions.
- So **new-session-per-task is the small-model analogue of compaction**: instead of compressing one long session to
  stay under a tiny window, you *close* it and let the project carry forward. (With a large-context model you'd keep
  one long session; the structure serves both.) Compaction and new sessions coexist — one keeps a session healthy,
  the other keeps the *project* moving.
- **Session coherence (to build):** because contexts are isolated, a session can hold a **stale view** of a file or of
  memory that another session, the user, or a tool has since changed. The answer is margin's own move applied to
  context: a **passive, derived, non-blocking cue** — "N files changed since this session read them," "memory updated"
  — calm and self-clearing on re-read. The principle is *never silently act on stale context* (the sibling of margin's
  never-lose-data). This is the one genuinely new thing to build here; isolated sessions are otherwise free.

## First run & progressive disclosure

The floor is **download → connect → chat**:

1. On launch, **auto-probe `http://localhost:8080`** (llama-server's default) — if a server is already on the machine,
   zero input.
2. Otherwise, one field: a **base URL** (`http(s)://host[:port][/path]` — a reverse-proxied or TLS-fronted server, or a bare `host:8080`, both work; an optional token lives in the OS keystore, never in config). Probe **`/props`**, which confirms the server *and*
   returns the model + context size, so the success state is honest: *"Found llama 3.2 3B · 8192 ctx."*
3. Land in **Chat mode**, cursor in the composer.
4. On failure, a **calm, specific** line (*"couldn't reach llama.cpp at `<addr>` — is `llama-server` running? default
   port is 8080"*) with retry — never a wall.

**"Do more" is progressive disclosure, not a tour.** The calm default earns the deeper features through clues: the
switcher's `Open folder…` nudges toward Projects; the status bar quietly teaches the budget; MCP / images / memory
surface in context. (This is "design to the floor" at the UX level — see [`architecture.md`](architecture.md) →
*Capability profile*.)

## The conversation pane

- The **thread** renders user / assistant / tool messages; assistant markdown renders the way margin's preview does.
- The **composer** uses margin's **live-styled markdown** — *"the formatting is the typing,"* markers visible but
  dimmed, no toolbar — and hosts `@`-mention.
- **Model + effort/thinking** selection lives in the top bar (margin's "view-mode control" slot). Effort is shaped to
  the model+config — honest control, never a fake uniform slider (see [`architecture.md`](architecture.md) →
  *Effort / "thinking"*); a model switch recomputes the budget.
- **Thinking** (`<think>` / `reasoning_content`) renders as a **collapsible, dimmed block** — margin's dimmed-markers
  aesthetic.

## Context budget — distributed, not a cockpit

The honest metering (see [`architecture.md`](architecture.md) → *Context management*) surfaces in three places, each
where it is contextual — **no dedicated inspector panel in v1**:

- **Ambient** — the status bar line plus a **color ramp as it fills** (calm neutral → amber → red): a peripheral
  signal you *feel* without reading, and at the edge the whole status bar can tint (margin's AppBar-tint move). For a
  small model this color *is* the prompt to wrap up, compact, or start a fresh session.
- **On tap** — a popover off the status bar: the segment stacked bar (system · instructions · memory · tools ·
  conversation · overhead) with counts, plus `Compact now`.
- **Inline in the thread** — compaction summaries as collapsible "summarized" badges (not a separate log); per-message
  **pin** (protect from compaction) and token cost on hover; staleness cues next to the file/memory they concern.

Distribute, don't centralize — margin's instinct that state lives where it is contextual (the conflict marker is in
the filename, not a dashboard). A dedicated context inspector is held in reserve only if the distributed version
proves too thin; it suits the small-model flow, where each short session's state is visible in-session at a glance.

## Tools & MCP

Configured MCP servers live in plain, **Claude-compatible** files — a **global** `~/.llamingo/mcp.json` and a
**per-project** `.mcp.json` in the working folder (see [`architecture.md`](architecture.md) → *MCP*). The UI is a
convenience over them, never a second source of truth.

- **A scope-aware Tools surface**, summoned as a dialog/slide-over (margin's Settings-dialog pattern) — never a
  permanent column. In a project it lists **global + that project's** servers and their tools; in Chats, just global —
  the switcher's scope model again.
- It shows each server's state plainly — **enabled · pending approval · blocked** — because a project's servers come
  from the repo and must clear the permission gate before first run, while global servers are trusted.
- **Access, not a slash command:** a visible `Tools…` in the top-bar overflow, and a jump-in from the budget
  popover's **tools** line — which already shows, honestly, what the active servers cost in context. Managing servers
  and managing that cost become one gesture (and on a small model, leaner tool defs mean a healthier window).

## Visual identity

- **Shared with margin: the sensibility.** Calm warm-neutral paper surface, flat, hairline borders, system light/dark,
  one accent used sparingly, translucent selection, mono for code. Re-implemented as a small web token set (not
  margin's code). This shared taste is what marks llaminGO as the same maker's work.
- **Own to llaminGO: the identity.** The Andean/Quechua heritage of the name (*llamingu*) lives in the **brand mark
  and the paper surface** — where margin puts its own cream warmth.
- **Functional accent stays clear of the semantic ramp.** Warm hues are already load-bearing (amber = budget warning,
  red = danger), so the brand/clickable accent is tuned to read unmistakably as "brand," not "alarm" — a muted
  ochre/clay if warm, kept distinct from amber and red.
- Exact hues are pinned and tested in both light and dark **at build time**; this document fixes direction, not values.

## Deferred / open

- **Dedicated context inspector** — only if the distributed budget proves thin.
- **`Sessions ⟷ Files` toggle** — later; v1 is conversations-only + `@`-mention.
- **Web UI framework** — lean toward Svelte (fits llama.cpp's own webui and the small-footprint goal); not settled.
- **Mobile** — the responsive web UI works now; the PWA ships with the v2 gateway.

## Changelog

- 2026-10-27: UX first pass settled — sensibility inherited from margin (calm, non-blocking, never-lose-data,
  no-hidden-state); shell (frameless two-panel + status bar, responsive, slide-over on mobile); left panel (scope
  switcher + flat session list, Chats as a peer scope + global quick-switcher, files via `@`-mention, no file tree);
  **sessions-as-context** strategy + **session-coherence** staleness cues; first-run (auto-probe `localhost:8080` →
  `/props` → Chat mode) + progressive disclosure; **distributed context budget** (ambient status bar + color ramp,
  on-tap popover, inline-in-thread; no inspector); **visual identity** (shared calm/paper sensibility with margin, own
  Andean-warm identity, functional accent kept clear of the alarm ramp).
- 2026-10-27: Added Tools & MCP — Claude-compatible global/project `mcp.json`, project servers gated (global trusted),
  a scope-aware Tools surface over the canonical files.

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
web UI everywhere, so the **Android app** reuses it wholesale (see [`architecture.md`](architecture.md) → *The core and its three faces*).

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
- **Web UI framework** — *settled:* **Svelte** (fits llama.cpp's own webui and the small-footprint goal).
- **Mobile** — a native **Android app** ships now (the responsive web UI in a WebView over a `gomobile`-bound core); broader mobile polish and off-LAN access ride later phases.

## Implementation gotchas (hard-won)

Small web-UI traps already paid for, kept so the next app doesn't re-learn them:

- **Animate to a literal, not a `var()`/`calc()`.** A CSS `transition` on `margin-left` (the sidebar
  collapse/slide) only animates when the target is a literal value; a `var(--sidebar-w)` or `calc()` target
  jumps. So the slide drives `margin-left` as an inline literal (`-${width}px`) from JS, not via a CSS var.
- **Pin the window-control cluster.** In the frameless merged top bar the min/max/close cluster is the last
  flex item; if the middle (title, pills) can't yield, the row overflows and the **close button falls off the
  right edge**. Fix: `flex-shrink: 0` on the cluster, and declutter the bar rather than letting it crowd.
- **Thin-window state must not clobber the saved preference.** Auto-hiding the sidebar on a thin window is a
  *display* concern; it must not overwrite the user's explicit open/closed choice. Keep two states: the
  **persisted** `sidebarOpen` (wide mode) and a **transient** `drawerOpen` (thin mode, never persisted). What
  shows is `narrow ? drawerOpen : sidebarOpen`, so growing the window back restores exactly what they had.
- **One breakpoint, in JS.** The thin/wide flag is computed in the resize handler and sets a `.narrow` class
  the CSS reacts to — a single source of truth, so the JS and CSS breakpoints can't drift.
- **Testing: the preview emulator doesn't fire `resize`.** Emulated viewport changes set `innerWidth` but don't
  dispatch a `window` `resize` event; dispatch one manually to test responsive logic. A real window fires it.

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
- 2026-10-29: Shipped the topbar declutter (Memory/Settings → ⋮ menu, dropped the duplicate model pill, pinned the
  window controls) and the thin-window **sidebar drawer** (hidden by default; toggle / swipe / scrim; breakpoint in
  JS; open/closed state preserved across thin↔wide). Added the *Implementation gotchas* section.
- 2026-10-29: Thin-window **bottom bar** — at ≤700px the status bar becomes the action strip (Chats button opens the
  drawer, Compact becomes an icon, the ⋮ menu moves down from the topbar and opens upward); wide layout unchanged.
- 2026-10-29 – 10-31: **Image generation UI** — an **Image models** section in Settings (fetch/refresh from Forge,
  per-model **auto-pick** + **default**, an expandable **recipe editor** for steps/CFG/sampler/size/modules), and a
  "Generating with ‹model›…" cue in the working indicator.
- 2026-10-31: **Tool-configuration surfaces** settled into three, deliberately distinct — **Settings** (⋮ menu;
  global: connection, global MCP servers, image models, display, debug), a per-chat **Tools** panel (⋮ menu; curate the
  chat's toolset — System vs grouped MCP servers, including a project's own), and a per-folder **Project** surface
  (⋮ menu, in a project session; system-tool defaults + the project's read-only `.mcp.json` servers). *Configuring*
  (global / per-folder) is separate from *activating* (per chat). The Settings modal scrolls as one styled pane; a
  chat-only **display toggle** hides the thinking + tool trail. **Model selection** is deferred to v2 (a plain
  `llama-server` serves one model).
- 2026-11-01: **Composer + connection controls** — the composer now **grows with input** (Shift+Enter for a newline),
  and a **Send button** with Enter-to-send gated to non-touch gives the mobile chat-app feel. A **Model/Connection
  picker** (status bar + a Settings card) selects among saved connections — each a **persona** (temperature + min_p +
  system prompt), its alias shown in the status bar. The **context budget** became model-aware: it tracks the active
  model's window, warns on overflow, and re-meters on a tool or model change.
- 2026-11-01: **Mobile UI** — the same responsive web UI runs on Android (a WebView over the in-process core).
  **Projects are hidden on the mobile client** (desktop minus Projects); the composer follows the WhatsApp model
  (Enter = newline on touch, the Send button sends); system bars are tinted to the UI's `--surface`.
- 2026-11-01: **File-tools surface** — a project session surfaces its **working directory** (with a toggle on the
  Project surface), supporting the gated file read/write/rename/copy/delete tools.

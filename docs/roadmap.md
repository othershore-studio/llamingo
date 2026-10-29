# llaminGO — Roadmap

Phased so nothing blocks on the hard parts. A real test suite and the testable-core / thin-shell split start at
increment #1.

## v1 — desktop + CLI, single machine (no server)

The whole product, usable solo, with **no server to run or secure and no sync engine to maintain**.

- **Core (Go package):** `Backend` seam (`LlamaCppNative`), chat API client with **SSE streaming**, context/token
  logic on the native API (`/props`, `/apply-template` + `/tokenize`, `/slots` when available).
- **One HTTP + SSE/WS adapter** over the core; **Wails** desktop shell points at it on localhost; **CLI** consumes the
  core package directly.
- **Honest context management:** segment breakdown + format-overhead line, threshold auto-compact, and
  **non-destructive compaction** (full history preserved; summaries shadow ranges).
- **Client-owned MCP loop** (`--jinja` tool-calls) — replaces the mcpo + front-end dependency.
- **Store:** two roots — `folder/` (project, Claude-Code-style) and `chat/` (free, the low-floor on-ramp); plain files
  per conversation; SQLite as a **rebuildable, self-healing index** only.
- **Memory** as a plain file (read path works on any model) + a **gated write-tool** to test on small models; the
  three-layer split from `AGENTS.md` / `CLAUDE.md` instructions.
- **Image generation** as the first **native tool** (direct forge/ComfyUI; an image MCP optional), behind the
  **permission gate** (built first, image gen as its one customer).
- **Capability-awareness** applied as a principle (honest, model-aware behavior; design to the floor).
- **Tests:** mocked-`llama-server` core tests (CI) + real-model capability probes (local).

Proves the full product without a server. **Cross-device is explicitly not here** — a single machine's store only;
LAN mobile can still hit `llama-server` for fresh inference, but shared history is a v2 capability.

### Build order (increments)

Dependency-ordered; each is test-first (the suite starts at #1), and the CLI is usable before the web UI exists:

1. **Native core + honest metering** — `Backend` seam + `LlamaCppNative` (`/props`, `/apply-template` + `/tokenize`,
   streaming `/v1/chat/completions`, `/slots` when present), the context builder + budget, proven against a
   **mocked `llama-server`** (recorded fixtures, GPU-free CI) and exercised by a thin CLI.
2. **Store** — two-root plain files, slug+hash keying, rebuildable/self-healing index.
3. **MCP loop** — client-owned tool-call loop; global + project `mcp.json`; mock-MCP tests.
4. **Permission gate + first native tool** — scope × risk gate (+ project-server trust), image generation via the
   `ImageGen` seam.
5. **The shell** — HTTP/SSE adapter + Wails desktop + the web UI (settles the UI framework).
6. **Context niceties + polish** — memory (read + gated write), `AGENTS.md`, compaction UX, effort/thinking,
   staleness cues, and the memory-integrity hash (review-on-mismatch; the SQLite db's one non-rebuildable trust state).

**Increment #1 slices**, each test-first:

- `1a` scaffolding + the mock server — module `github.com/othershore-studio/llamingo`, Go 1.24, MPL-2.0 headers,
  `go test` CI, a stdlib `httptest.Server` replaying recorded fixtures.
- `1b` the `Backend` seam (`Props` / `Tokenize` / `ApplyTemplate` / `ChatStream`).
- `1c` read endpoints (`/props`, `/tokenize`, `/apply-template`).
- `1d` streaming chat + SSE parse (content deltas, end-of-stream `usage`, `tool_calls` / `reasoning_content` shapes
  detected but not yet acted on).
- `1e` the context builder + segment breakdown + a `format overhead` line, reconciled against the authoritative
  `usage`.
- `1f` connect / auto-probe + a minimal CLI + a **live smoke test against a real `llama-server`**, which also supplies
  the `1c`/`1d` fixtures.

**Done:** `go test ./...` green and GPU-free (all mocked), and the CLI holds a streamed exchange against a real
`llama-server` with a budget matching its reported `usage`.

## Status (2026-10-29)

Increments #1–#6 are built and merged — core + honest metering, the two-root store, the client-owned MCP loop and
permission gate (proven by image gen), the shell (CLI → HTTP/SSE adapter → Svelte web UI → Wails desktop), and #6's
context niceties: live thinking, **compaction** (manual + auto, non-destructive projection), the **memory foundation**
(injected as a metered segment, user-editable, tamper-evident hash), and **server/staleness monitoring**. The earlier
real-model fixes are merged too: staleness cues; the desktop loopback-API server (`127.0.0.1:8766` + CORS, since
Wails' asset server can't flush SSE); and safe markdown rendering (markdown-it, `html:false`).

**#6's capstone — the agentic tool loop — is done.** `mcp.Run` drives the chat stream rather than a bare completion:
the model can call a tool mid-turn, mediated by the conversation's permission gate, with the **SSE permission
handshake** surfacing the consent prompt on the stream and the answer returning on `POST /api/permission/{id}`.
`write_memory` is live through it — gated, tested end to end (allow and deny paths).

**Native tools and MCP servers are wired-ready seams:** image gen and MCP servers plug into the same Registry as
`write_memory`, but a live turn currently offers only `write_memory`. Lighting up the rest is the next tool step —
plumbing, now that the loop + handshake exist.

**Post-#6 polish, merged 2026-10-29:** connection config (persisted llama-server URL/token + a Settings panel + live
hot-swap); a topbar declutter (⋮ menu, pinned window controls); a responsive thin-window layout (sidebar drawer with
state preservation + a bottom action bar); compaction UX (an in-thread "Compacting…" cue + the summary rendered where
it ran, with a timestamp); readable speaker labels in `conversation.md`; and the window title tracking the active
chat. See [`ui.md`](ui.md).

**Remaining threads:** light up image gen + MCP servers in a live turn; small niceties (friendly model name via
`--alias`, native `time`/`location` tools, compaction token-savings on the summary marker); model selection moving to
the bottom bar; then v2 — the gateway.

## Backlog — surfaced by real-model testing (2026-10)

Captured while using the app against the real model; none blocking, ordered loosely. **Most of the UI/UX items below
shipped 2026-10-29** (responsive thin-window layout, frameless window + title bar, per-message copy, rename, tokens/sec)
— see [`ui.md`](ui.md) and the Status above; the ones still open are compaction token-savings display and model
configuration niceties.

**UI / UX**

- **Responsive to window resizing** — the two-panel layout at narrow/wide sizes (toward the v2 PWA / mobile).
- **Frameless window + custom title bar** — Wails `Frameless` + a `--wails-draggable` header (like margin). The chrome
  is just HTML, so this is cheap.
- **Per-message actions** — hover tools on each message (copy, …); fenced code blocks get a copy button. (The HTML UI
  already makes everything selectable/copyable — a deliberate win of the web-UI architecture.)
- **Compaction values on return** — show the stored `replaced`/`summary` token savings on the summary block when a
  chat is reopened (today only in the transient toast; the counts are already on disk in the summary entry).
- **Rename chats/sessions** — inline title edit (`PUT` the title to `meta.json`). Later: suggested names, **opt-in and
  off the hot path** (a background title-generation call is exactly the OpenWebUI tax to avoid).
- **tokens/sec readout** — llama.cpp returns `timings` in the `/v1/chat/completions` final chunk (prompt/predict
  tokens + ms + per-second rates); surface it beside the budget. Shares the terminal-`Delta` plumbing with the tools
  work. Honest metering extended to speed.

**Model / backend**

- **Friendly model name** — prefer llama.cpp's `model_alias` from `/props` (set with `--alias`) over the path-derived
  name.
- **Model configuration UI** — later; `LLAMINGO_URL` via env var is fine during development.

**Native tools (local, gated)**

- **image generation** (seam already built), plus **time** and **location**. `time` is trivial; **location** needs a
  deliberate design — source (configured / IP / OS) and privacy (explicit, gated like any tool).

**Guiding principle:** keep it lean, fast, and transparent — this simplicity (and speed: a lean prompt, no background
model calls) is preferred over OpenWebUI's feature-heaviness. Everything added stays opt-in and surfaces its cost.

## v2 — the gateway (remote + mobile + sync)

The **optional** small server that earns its place only when you leave the LAN.

- Auth + reverse-proxy to llama.cpp + **application-layer sync** of the `chat/` root (data only; each device rebuilds
  its own index — the DB is never on the wire). Dockerable / `.service`-friendly; bearer-token, auth-proxy posture, no
  OAuth inside.
- Serves the same web UI as a **PWA** → mobile without a native-app lift.
- The same HTTP/SSE adapter from v1, exposed and authenticated — a deploy mode, not a rewrite.
- *(Open: syncing the `chat/` root is the leaning; not finally committed.)*
- **Client-compat exploration:** survey other popular clients' project formats and offer a per-project "compatible
  with" setting. (v1 targets Claude, whose `.mcp.json` + `AGENTS.md` already give a frictionless hand-off to a
  frontier model when a local task outgrows the window.)

## Later — local tools (permission-gated)

- More native/local tools (Python, shell, filesystem), each declaring its risk tier into the **gate that already
  exists** (built and proven by image gen in v1). Build the gate before the tools — done in v1.

## Even later / maybe never

- **Remoting `folder/` (project) sessions.** Intrinsically machine-local (they reference a filesystem path); would
  require projects to carry a stable, path-independent id. v3/v4/never.
- A true native mobile app (Tauri 2) if the PWA proves insufficient.

## Non-goals

- Model downloading / training (assumes a running llama.cpp).
- **A client-side sync engine** — plain files + the user's own sync tool, or the v2 gateway.
- Being a general "any backend" client (OpenAI-compatibility is a side effect, not a target).
- Replacing frontier models — the point is to make local good enough to carry the routine load and save frontier
  tokens.

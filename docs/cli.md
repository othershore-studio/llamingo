# llaminGO — CLI / TUI (`llamingo-code`)

The terminal face of llaminGO: a **fully-Go TUI** you run in any directory, and it opens
a session anchored to that folder — the *same* project the desktop app would open. It is
the one shell still thin; everything it needs (core client, honest context budget, the
two-root store, the client-owned MCP loop, the permission gate, the file + image tools)
already exists and is shared.

> **Status:** v1 built and merged to `main`. Anchored streaming chat, the permission gate,
> per-chat tool curation, memory/prompt inspection, scroll, markdown rendering, session
> continuity (`-c` / `/new` / `/resume`), and defaulting to the active saved connection all
> work. Still to come in the TUI: image generation and MCP servers (the native system tools
> are wired; these two aren't yet), `/compact`, terminal images, the launchers, and first-run
> PATH registration. A terminal front-end over the core, not a rewrite — the same payoff as
> Wails being "a window + a localhost server."

## The shape

`llamingo` in `~/Code/foo`:

1. canonicalizes the cwd and computes the project key `folder/<slug>-<hash8>`,
2. finds or creates `~/.llamingo/folder/<key>/` for that path,
3. loads that project's `AGENTS.md`/`CLAUDE.md`, `.mcp.json` (gated servers),
   `.llamingo/project.json` system-tool defaults, and its `memory.md`,
4. connects to the configured `llama-server` (`LLAMINGO_URL` / saved config, `/props` probe),
5. opens an interactive TUI session **in that project's context**.

It is the **same project the desktop opens for that folder** — open a folder in the TUI or
in the desktop and you land in the identical project: shared history, shared `AGENTS.md`,
`.mcp.json`, `.llamingo/project.json`, and `memory.md`. The **global config is shared too** —
connections/personas, global MCP servers, and settings all live under `~/.llamingo`, so the
TUI and the desktop read the same ones. Configure once, use from either; start a session in
the terminal and pick it up in the desktop, or vice-versa. **No server:** the CLI consumes
the core package *directly* (the architecture's "CLI direct" face — see
[`architecture.md`](architecture.md)), so a TUI session needs no `llamingo-serve` running.

**Invocation**

- `llamingo` — interactive TUI, anchored to cwd (a fresh session; `/resume` opens a past one).
- `llamingo -c` — resume this project's most recent session (the `claude -c` move).
- `llamingo -m "…"` — scriptable one-shot, non-interactive (today's behavior, kept).
- **Connection:** with no `-url`, the TUI uses the **active saved connection** from the shared
  config — its URL, token, sampling, and persona; `-url` / `LLAMINGO_URL` override.
- `-token`, `-system`, `-data-dir` as today. (Anchoring elsewhere than cwd — a `-C <dir>` — is
  not built yet.)

## Fully Go, cross-platform

One **static Go binary per OS/arch**, no runtime deps. The core already cross-compiles
**CGO-free** to linux/arm64 and android/arm64 (proven by the mobile `.aar`), and a CGO-free
Go binary makes raw syscalls — no libc — so the same static arm64 build runs on Android's
Bionic. Targets:

- **Termux (Android, arm64)** — a real terminal; the TUI just runs. *This* is where Termux
  belongs — a first-class home for the TUI, not the GUI forced through it.
- **Linux / macOS** — native terminals.
- **Windows** — Windows Terminal / modern conhost.

Detect terminal capabilities (color depth, true-color, inline-image protocol) and degrade
gracefully across them.

## The TUI

**DECISION — framework: `tview`** (on `gdamore/tcell`). Chosen over Bubble Tea after weighing
both: tview is a widget toolkit (lists, forms, modals, flex layout, a built-in Image
primitive) rather than an Elm-style render loop, so the pickers and the consent modal fall out
of its primitives; it is pure-Go / no-cgo (the CGO-free cross-compile holds), MIT, and proven
in k9s / lazysql / podman-tui. It *is* a real dependency — but a **load-bearing, justified**
one, the TUI's analogue of Wails for the desktop. (Markdown is rendered with `yuin/goldmark`,
also pure-Go / MIT.)

**Layout** — one screen to start (not a window manager; grows toward menus later):

- a **transcript viewport** — scrollable (PgUp/PgDn); user / assistant / tool messages, with
  markdown rendered for the terminal and the tool-trail inline. Streamed raw for
  responsiveness, then repainted as rendered markdown once a turn completes.
- an **input line** — Enter sends (single-line today; a multi-line composer with
  Shift/Alt+Enter is a later nicety),
- a **status bar** — project · model · the honest **context budget**,
- **modals** over the transcript for the **permission prompt**, the **session picker**
  (`/resume`), and the **tools view** (`/tools`).

**Commands (slash).** A TUI has no ⋮ menu or buttons, so the command surface is **slash
commands** typed in the composer (the `claude-code` idiom) — each the terminal equivalent of
a desktop surface:

- **Built:** `/new` (fresh session) · `/resume` (session picker, newest first) · `/tools`
  (curate this chat's toolset, persisted per-chat) · `/memory` (show saved memory) ·
  `/prompt` (show the system prompt the model receives) · `/clear` · `/help` · `/exit`
  (`/quit`). Keybindings: Esc closes a modal, PgUp/PgDn scroll, Ctrl+C quits.
- **Planned:** `/model` or `/connection` (switch persona without leaving the terminal) ·
  `/compact` · `/project` · `/thinking`. `/tools` curates the System tools today and will show
  MCP servers' tools once those are wired into the TUI. There is no editor for `.mcp.json`
  itself — that's a file you edit (global `~/.llamingo/mcp.json`, or the project's `.mcp.json`)
  — the TUI *reads and curates* what those servers declare, exactly as the desktop does.

Streaming tokens interleave with the live budget and the MCP loop (a core concern from commit
one, same as everywhere). **Terminal images** — the genuinely hard part, still being decided:
the plan is to detect Kitty / iTerm / sixel and draw inline, else print the saved path; which
protocols the actual terminals (Windows Terminal, Termux, iTerm, …) support will shape it.

## The permission gate in the terminal

The gate's `Prompter` is the seam. The desktop implements it as the **SSE handshake**; the
TUI implements it as a **blocking terminal modal** — same scopes (once / this-session /
persistent), same risk-driven friction, same *specific* confirmation ("run this Python:
…"). Per-conversation gates, grants to the same plain file. No new gate logic — just a
second `Prompter`.

## No install — safe, user-context only

A load-bearing principle, not just packaging: the CLI **never needs an installer or admin
rights**. The binary is portable — copy it anywhere — and everything it writes stays in the
**user's own context**: `~/.llamingo` for data, the *user* PATH (never the system PATH), the
per-user `HKCU` registry on Windows. No elevation, no system directories, no UAC. Drop the
file, run it, done.

**Naming**

- **Command: `llamingo`** — what you type in a directory. Binary `llamingo`
  (`llamingo.exe` on Windows), built from `cmd/llamingo` (which today holds the one-shot CLI).
- **"llamingo-code"** is the affectionate name for this project-anchored mode (the
  `claude` → `claude-code` echo). Ship the binary inside a **`llamingo-code/` folder** you
  drop anywhere and add to PATH — mirroring how you run claude-code. No installer; it is a
  single file.
- **Coexists with the desktop.** The desktop app is its own Wails binary, launched by icon
  (it needn't be on PATH); the TUI is the PATH binary. Same base name is fine — different
  paths, like Claude's two `claude.exe`.
- **GitHub Releases** ship the per-OS/arch CLI binaries beside the desktop + Android
  artifacts: the release workflow gains a `go build` matrix (windows/amd64, linux/amd64,
  linux/arm64, darwin/amd64, darwin/arm64, and the Termux arm64 build) — cheap, and a CLI
  binary needs no signing.

### First run — PATH self-registration (consent-gated, self-healing)

So "type `llamingo` anywhere" works without the user hand-editing PATH, the binary registers
*itself* — in the gate's own spirit (a side effect → ask first, be specific, be reversible):

- **On first run**, check whether the binary's own directory is on the **user** PATH. If not,
  ask: *"Add this folder to your PATH so `llamingo` runs from anywhere? (your user PATH only —
  no admin)."* On yes, add it (Windows: `HKCU\Environment` + a `WM_SETTINGCHANGE` broadcast;
  Unix: a line in the shell rc, or a symlink into `~/.local/bin`). Decline and nothing changes
  — you just run it by path.
- **Self-heal:** if a llaminGO entry is already on PATH but points at a *different* directory
  than the running binary (you moved the folder), offer to **repoint it** to the current
  location — so moving the install never breaks the command or leaves a stale, dead entry.
- All **user-context, no admin, reversible** — the no-install posture, applied to PATH.

## The launch trick (shipped as a convenience, opt-in)

PATH self-registration already covers "run `llamingo` from any terminal"; this adds the
**Explorer right-click → open here** convenience on top. Mirrors your `claudeCode.reg`,
generalized and shipped in the repo (e.g. `cli/launchers/`):

- **Windows** — a `llamingo-code.reg` adding a per-user (`HKCU`) Explorer context menu on
  folders **and folder background**:
  - `Directory\shell\LlamingoCode` → *"Open with llaminGO"* →
    `wt.exe -d "%V" powershell -NoExit -Command llamingo` (Windows Terminal in the clicked
    folder, running the TUI), with our brand icon.
  - a *"…(resume last)"* variant → `llamingo -c` (your `claude -c` pattern).
  - also `Directory\Background\shell\…` so right-clicking the empty space *inside* a folder
    works too (your current `.reg` only fires on right-click *onto* a folder).
  - `wt.exe` as the nice default, with a `conhost`/`powershell` fallback note for Win10
    without Windows Terminal.
- **Termux / Linux / macOS** — a one-liner install script that drops the binary on PATH
  (and, optionally, a `llamingo` shell function); on Linux desktops, an optional
  file-manager *"Open in llaminGO"* action (`.desktop` / Nautilus script) — the `.reg`'s
  cross-platform cousins.

These are **conveniences, never requirements** — the floor is "put the binary on PATH,
type `llamingo`."

## v1 scope (test-first, grow from a floor)

Same discipline as the rest — every slice runs against the mocked `llama-server`
(`internal/lcpptest`), GPU-free in CI:

1. **Anchored chat** — ✅ `llamingo` in a dir → project resolve/create → interactive streaming
   chat with the honest budget in the status bar. The floor: the TUI shell over the core.
2. **The gate** — ✅ the terminal `Prompter` (once / this-chat / always), so the native file +
   memory tools work exactly as elsewhere. (Image gen + MCP servers: still to wire in.)
3. **Niceties** — ✅ `-c` resume + `/resume` picker + `/new`; the thinking spinner + tool-trail;
   **markdown rendering**; the `/tools` curation view; `/memory` + `/prompt` inspection; the
   connection-from-config default. Still open: `/compact`, terminal images.
4. **Polish** — ⏳ first-run PATH self-registration (consent-gated, self-healing), the launchers
   (`.reg` + shell), the release-matrix binaries.

## Decided / still open

- **TUI framework** — ✅ decided: `tview` (see above).
- **Command name** — ✅ `llamingo` (the binary); "llamingo-code" stays the affectionate name
  for the project-anchored mode.
- **Resume semantics** — ✅ `-c` = this project's most recent session; `/resume` picks the rest,
  newest first. Matches `claude`'s feel.
- **Still open** — in-TUI connection/model switching (`/model`); image gen + MCP in the TUI;
  terminal images (the hard one); a richer multi-pane layout vs the current chat + modals.

## Changelog

- 2026-11-02: **v1 built and merged.** The spec is real: `tview` chosen (over Bubble Tea);
  anchored streaming chat + honest budget + thinking spinner; the permission gate as a consent
  modal; native tools via the client-owned loop; `/tools` curation; `/memory` + `/prompt`
  inspection; PgUp/PgDn scroll; markdown rendering (goldmark); session continuity (`-c` /
  `/new` / `/resume` + history loading); and defaulting to the active saved connection.
  Pending: image gen + MCP in the TUI, `/compact`, terminal images, launchers, PATH registration.
- 2026-11-01: Spec drafted — a fully-Go, cross-platform (Termux / Windows / Linux / macOS)
  project-anchored TUI over the shared core; Bubble Tea proposed; **no-install, user-context
  only** (portable binary, user PATH, no admin) with **consent-gated, self-healing first-run
  PATH registration**; naming (`llamingo`, "llamingo-code" the mode); the Windows `.reg` +
  shell launchers mirroring Daniel's `claudeCode.reg`; a test-first v1 scope from an
  anchored-chat floor.

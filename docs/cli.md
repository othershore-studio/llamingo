# llaminGO — CLI / TUI (`llamingo-code`)

The terminal face of llaminGO: a **fully-Go TUI** you run in any directory, and it opens
a session anchored to that folder — the *same* project the desktop app would open. It is
the one shell still thin; everything it needs (core client, honest context budget, the
two-root store, the client-owned MCP loop, the permission gate, the file + image tools)
already exists and is shared.

> **Status:** shipped and merged to `main`, matching the desktop's feature surface. Anchored
> streaming chat, the permission gate, per-chat tool curation, memory/prompt inspection,
> scroll, markdown rendering, session continuity (`-c` / `/new` / `/resume`), **inline terminal
> images**, and **image generation + MCP servers** (full tool parity with the desktop). A
> **connection manager** (`/connections`) and a **settings hub** (`/settings`: connections ·
> Forge · global MCP · a DNS resolver) configure it all — Forge and MCP edits apply **live, no
> restart** — so a headless / CLI-only box
> is set up with no GUI. Windows PATH + Explorer-menu self-registration ships as
> `llamingo -setup`; the per-OS/arch binaries ship from [`cli.yml`](../.github/workflows/cli.yml).
> Still open: `/compact` and a richer multi-pane layout. A terminal front-end over the core,
> not a rewrite — the same payoff as Wails being "a window + a localhost server."

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

**DNS on Android/Termux.** A CGO-free Go binary uses Go's own resolver, which reads
`/etc/resolv.conf` — Android has no usable one, so hostname lookups fall back to `[::1]:53`
and fail (direct IPs still work; the browser is fine, because it uses the system resolver a
non-cgo binary can't reach). Rather than switch to CGO + the Android NDK — which would cost
the toolchain-free one-minute cross-compile — llamingo takes an optional **DNS server
setting**: `-dns`, `LLAMINGO_DNS`, or the persisted `dns` in config (set it from `/settings` →
*DNS resolver*). Point it at any resolver that knows your names — a LAN resolver, or a public
one if your private addresses are published there (as with a mesh-VPN + public-DNS setup). It
is inert on every platform where DNS already works.

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
- a **composer** — a multi-line input: Enter sends, Ctrl+J (or Shift/Alt+Enter where the
  terminal reports the modifier) inserts a newline; it grows with its content. Mouse is **off
  by default** so the terminal's own select-and-copy works; `/mouse` turns on wheel-scroll,
- a **status bar** — project · the active connection's alias · the honest **context budget**
  (coloured by fill level),
- **modals** over the transcript for the **permission prompt**, the pickers (`/resume`,
  `/model`), the **tools view** (`/tools`), the **config editors** (`/settings`,
  `/connections`), the **image viewer** (`/image`), and the **thinking view** (`/thought`).

**Commands (slash).** A TUI has no ⋮ menu or buttons, so the command surface is **slash
commands** typed in the composer (the `claude-code` idiom) — each the terminal equivalent of
a desktop surface:

- **Built:** `/new` · `/resume` (session picker) · `/model` (alias `/connection`: switch the
  session's connection/persona) · `/connections` (add/edit/activate/delete saved connections) ·
  `/settings` (alias `/config`: the hub — connections · image gen (Forge) · global MCP · DNS resolver) ·
  `/tools` (curate this chat's toolset, System **and** MCP, persisted per-chat) · `/memory` ·
  `/prompt` · `/thought` (the last turn's thinking) · `/image` (view generated images) ·
  `/mouse` (toggle wheel-scroll) · `/clear` · `/help` · `/exit` (`/quit`). Keybindings: Esc
  closes a modal, PgUp/PgDn scroll, Ctrl+C quits.
- **Still planned:** `/compact` (manual compaction) and `/project`.

MCP servers' tools *do* show in `/tools` now, and **`/settings` edits the global
`~/.llamingo/mcp.json`** (add/edit/delete servers), the Forge image config, and a **DNS resolver**
(Android/Termux — see below) — the headless analogue of the desktop Settings. MCP and Forge edits
apply live; the DNS setting is read at startup. A project's own `.mcp.json` stays a file you
edit in the repo; the TUI reads and curates (gated) what it declares.

Streaming tokens interleave with the live budget and the MCP loop. **Terminal images** —
shipped: generated images render inline in the transcript (half-block, doubled vertical
resolution) and `/image` opens a full viewer (truecolor blocks, or sixel where the terminal
supports it). Over SSH, set `COLORTERM=truecolor` on the server for full colour — OpenSSH
doesn't forward it, so tcell otherwise falls back to 256 colours (banding).

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

### First run — PATH + Explorer self-registration (consent-gated, self-healing)

**Shipped as `llamingo -setup`** (Windows): run it once and it adds the binary to your user
PATH and an Explorer right-click entry, **"Call a llaminGO here"** (+ a "(resume)" variant);
a startup hint nudges you to run it. On **Windows 11** the entry is in the classic context
menu — **Shift + right-click** (or "Show more options"). It self-heals: re-run it after moving
the binary and it repoints everything. The design, in the gate's own spirit (a side effect →
ask first, be specific, be reversible):

- **On first run**, check whether the binary's own directory is on the **user** PATH. If not,
  ask: *"Add this folder to your PATH so `llamingo` runs from anywhere? (your user PATH only —
  no admin)."* On yes, add it (Windows: `HKCU\Environment` + a `WM_SETTINGCHANGE` broadcast;
  Unix: a line in the shell rc, or a symlink into `~/.local/bin`). Decline and nothing changes
  — you just run it by path.
- **Self-heal:** if a llaminGO entry is already on PATH but points at a *different* directory
  than the running binary (you moved the folder), offer to **repoint it** to the current
  location — so moving the install never breaks the command or leaves a stale, dead entry.
- All **user-context, no admin, reversible** — the no-install posture, applied to PATH.

## The launch trick (shipped in `-setup`, opt-in)

The Explorer right-click → open-here convenience ships **inside `llamingo -setup`** (above),
not as a separate `.reg` file — setup writes the per-user (`HKCU`) registry entries directly
and heals them. The design it realizes (generalizing your `claudeCode.reg`):

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
2. **The gate** — ✅ the terminal `Prompter` (once / this-chat / always), so the native file,
   memory, **MCP, and image-generation** tools all work behind it. ✅ full tool parity.
3. **Niceties** — ✅ `-c` resume + `/resume` picker + `/new`; the thinking spinner + tool-trail;
   **markdown rendering**; the `/tools` curation view (System + MCP); `/memory` + `/prompt` +
   `/thought`; **inline terminal images**; the connection-from-config default. Still open:
   `/compact`.
4. **Config surface** — ✅ `/connections` and the `/settings` hub (connections · Forge · global
   MCP · a DNS resolver); Forge/MCP applied **live** — a headless box set up with no GUI.
5. **Polish** — ✅ Windows PATH + Explorer-menu self-registration (`llamingo -setup`,
   consent-gated, self-healing, embedded icon); ✅ the release-matrix binaries
   ([`cli.yml`](../.github/workflows/cli.yml)).

## Decided / still open

- **TUI framework** — ✅ decided: `tview` (see above).
- **Command name** — ✅ `llamingo` (the binary); "llamingo-code" stays the affectionate name
  for the project-anchored mode.
- **Resume semantics** — ✅ `-c` = this project's most recent session; `/resume` picks the rest,
  newest first. Matches `claude`'s feel.
- **Done since** — `/model` switching, `/connections` + the `/settings` config hub (+ a DNS
  resolver), live-apply of Forge/MCP, image gen + MCP parity, inline terminal images, and
  `-setup` all shipped.
- **Still open** — `/compact`; a richer multi-pane layout vs the current chat + modals; a
  `-C <dir>` to anchor somewhere other than cwd.

## Changelog

- 2026-11-03 (v0.3.1): **Headless hardening.** A **DNS resolver** setting (`-dns` / `LLAMINGO_DNS` /
  a persisted `dns`, set via `/settings`) so hostnames resolve on Android/Termux, where Go's own
  resolver finds no nameserver; plus a guide section documenting the SSH + Termux gotchas (colours,
  `noexec`, DNS, CA certs, `sshd`) with exact commands. Validated on a phone over Termux.
- 2026-11-02 (later): **Feature-complete vs the desktop, and headless-ready.** Shipped since
  the morning's v1: **image generation + MCP** tool parity; **inline terminal images** (+ the
  `/image` viewer); `/model` connection switching; a **`/connections`** manager and a
  **`/settings`** hub (connections · Forge · global MCP) that configure everything **live, no
  restart**; **`llamingo -setup`** (Windows PATH + Explorer right-click, self-healing, embedded
  icon); and the **per-OS/arch release binaries** ([`cli.yml`](../.github/workflows/cli.yml)).
  Validated headless over SSH on a real server, image generation included. Still open:
  `/compact`, a richer multi-pane layout.
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

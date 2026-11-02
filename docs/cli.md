# llaminGO — CLI / TUI (`llamingo-code`)

The terminal face of llaminGO: a **fully-Go TUI** you run in any directory, and it opens
a session anchored to that folder — the *same* project the desktop app would open. It is
the one shell still thin; everything it needs (core client, honest context budget, the
two-root store, the client-owned MCP loop, the permission gate, the file + image tools)
already exists and is shared.

> **Status:** spec, not built. The core is done; this is a terminal front-end over it,
> not a rewrite — the same payoff as Wails being "a window + a localhost server."

## The shape

`llamingo` in `~/Code/foo`:

1. canonicalizes the cwd and computes the project key `folder/<slug>-<hash8>`,
2. finds or creates `~/.llamingo/folder/<key>/` for that path,
3. loads that project's `AGENTS.md`/`CLAUDE.md`, `.mcp.json` (gated servers),
   `.llamingo/project.json` system-tool defaults, and its `memory.md`,
4. connects to the configured `llama-server` (`LLAMINGO_URL` / saved config, `/props` probe),
5. opens an interactive TUI session **in that project's context**.

It is the **same project the desktop opens for that folder** — so you start a session in
the terminal and pick it up in the desktop, or vice-versa; shared history, shared config.
**No server:** the CLI consumes the core package *directly* (the architecture's "CLI
direct" face — see [`architecture.md`](architecture.md)), so a TUI session needs no
`llamingo-serve` running.

**Invocation**

- `llamingo` — interactive TUI, anchored to cwd (new session, or a resume picker).
- `llamingo -c` — resume the last session in this project (the `claude -c` move).
- `llamingo -m "…"` — scriptable one-shot, non-interactive (today's behavior, kept).
- `llamingo -C <dir>` — anchor somewhere other than cwd.
- `-url` / `LLAMINGO_URL`, `-data-dir`, … as today.

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

**DECISION — framework:** recommend **Bubble Tea** (+ Lipgloss for styling, Bubbles for
the viewport/textarea). It is the de-facto Go TUI framework, actively maintained, runs on
all four targets, and brings "windows and menus in a terminal" back. It *is* a real
dependency — but a **load-bearing, justified** one, the TUI's analogue of Wails for the
desktop, not feature-bloat. (Alternatives: `tview` — more classic widgets; or lean
stdlib + raw ANSI — leanest, but you hand-roll raw-mode, resize, input parsing and still
want a viewport. Your call; the layout below assumes Bubble Tea.)

**Layout** — one screen to start (not a window manager; grows toward menus later):

- a **transcript viewport** — scrollable; user / assistant / tool messages; markdown
  rendered for the terminal; thinking as a dimmed collapsible block; the tool-trail inline,
- an **input line** — multi-line; Enter sends, Shift/Alt+Enter for a newline (the terminal
  analogue of the composer),
- a **status bar** — connection · model/alias · the honest **context budget** with its
  color ramp (calm → amber → red),
- **modals** over the transcript for: the **permission prompt**, a **session picker**, and
  later a **menu** (the ⋮ equivalent: tools · project · settings).

Streaming tokens interleave with the live budget and the MCP loop (a core concern from
commit one, same as everywhere). **Terminal images** (image-gen output, attachments):
detect Kitty / iTerm / sixel and draw inline; else print a path/URL.

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

1. **Anchored chat** — `llamingo` in a dir → project resolve/create → interactive streaming
   chat with the honest budget in the status bar. The floor: proves the TUI shell over the
   core.
2. **The gate** — the terminal `Prompter` (allow / once / session), so the file tools, image
   gen and MCP servers work exactly as elsewhere.
3. **Niceties** — `-c` resume + session picker; thinking / tool-trail rendering; terminal
   images; the menu (tools · project · settings).
4. **Polish** — first-run PATH self-registration (consent-gated, self-healing), the
   launchers (`.reg` + shell), the release-matrix binaries.

## Open decisions (your redline)

- **TUI framework** — Bubble Tea (recommended) vs `tview` vs lean-stdlib: the
  dependency-vs-polish call, weighed against [`prefers-lean`].
- **Command name** — `llamingo` (assumed), or literally `llamingo-code`?
- **How much "windows and menus"** — a clean chat + status + modals for v1, vs a richer
  multi-pane TUI (a session-list pane, etc.) later.
- **Resume semantics** — `-c` = last session in this project, plus a picker for the rest;
  match `claude`'s feel?

## Changelog

- 2026-11-01: Spec drafted — a fully-Go, cross-platform (Termux / Windows / Linux / macOS)
  project-anchored TUI over the shared core; Bubble Tea proposed; **no-install, user-context
  only** (portable binary, user PATH, no admin) with **consent-gated, self-healing first-run
  PATH registration**; naming (`llamingo`, "llamingo-code" the mode); the Windows `.reg` +
  shell launchers mirroring Daniel's `claudeCode.reg`; a test-first v1 scope from an
  anchored-chat floor.

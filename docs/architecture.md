# llaminGO — Architecture

Design notes for the client. The guiding principle is a **testable core + thin shells**: one engine-agnostic-ish core,
with UI/CLI/gateway as replaceable surfaces on top.

## Scope statement

- **Target backend:** llama.cpp (`llama-server`). Its **native API is the statement** — we use it where it beats the
  OpenAI veneer. This would only change if another engine becomes dominant *and* the whole stack moves with it.
- **Stays OpenAI-compatible** so it *can* point at other servers, but that is not a goal and will not drive design.

## The three faces — shared core + thin shells

One **shared Go core** (the llama.cpp API client, the MCP loop, the store format, the token/context logic) with thin
shells on top:

- **Desktop** — [Wails](https://wails.io/) (Go backend + system webview) wrapping a web UI. Small single binary.
- **Mobile** — the *same* web UI served as a PWA by the gateway (v2). A true native app (via Tauri 2 or similar) is an
  option only if the PWA proves insufficient.
- **CLI** — another shell on the same core (not the web UI). Terminal images: capability-detect (Kitty / iTerm / sixel),
  else print a path/URL.

Keeping every UI a **thin client of the core's API** makes the desktop-shell framework choice reversible rather than
load-bearing.

## Backend seam

All engine access goes through a single `Backend` interface, with **`LlamaCppNative` as the only implementation**. This
is not multi-backend support — it is one boundary so a future engine swap is a contained module change, not a
grep-through-the-codebase rewrite.

## The two problems: storage-sync ≠ remote-access

These are **separate concerns**. Conflating them is what makes "do I need a server?" feel hard. Split, it answers itself.

### Storage sync (no server required)

- **Bring-your-own backend:** local folder / WebDAV / SFTP / git. The client is backend-agnostic for storage.
- **Canonical state = plain files** (per-conversation, per-project; Markdown-with-frontmatter or JSON; git-diffable,
  human-readable, portable).
- **SQLite, if used, is a rebuildable index/cache — never the sync unit** (a monolithic DB over a file-sync store
  corrupts and kills the "it's just a folder" sovereignty).
- **Eventual sync, not a live mirror.** "Chat on desktop, continue on mobile later" — file sync delivers that.
- **Last-writer-wins** is acceptable: file-per-conversation makes real conflicts rare and human-resolvable (no CRDT).

### Remote access (the optional gateway)

- A **small optional server** — auth + reverse-proxy to llama.cpp + optional sync relay. Dockerable / `.service`-friendly.
- It is a **gateway, not a sync database** — the single internet-reachable endpoint, mirroring the "auth proxy in front"
  posture.
- **Off-LAN mobile forces it. Desktop and LAN mobile (a home tablet/phone) do not** — they reach llama.cpp directly on
  the LAN and sync via a LAN file store.

## Why llama.cpp *native* (the differentiator)

A generic OpenAI-API client can't do these; a llama.cpp-focused one can:

- `/props` → real context size; `/tokenize` → **exact** token counts; `/slots` → KV-cache state. Together: honest,
  accurate **context management and token metering** (a visible, truthful budget — not a hidden window).
- `--jinja` → the server parses the model's chat template and emits OpenAI-style `tool_calls`.
- GBNF grammars for constrained output, etc.

## MCP — the client owns the loop

With `--jinja` emitting `tool_calls`, the client runs the full loop itself:

> client ↔ MCP (stdio/HTTP) to its own servers · client ↔ chat API to `llama-server` · get `tool_calls` · execute the
> MCP tool · feed the result back · repeat.

This removes any dependency on an OpenAPI bridge (mcpo) or a separate chat front-end for tool use.

## Features

- Context management · chat history · projects-as-folders.
- MCP (first-class).
- Image generation via MCP (forge/ComfyUI) or a native connection.
- **Later: local tools, permission-gated.** Build the tools later, but design the **permission gate as a first-class
  concept from day one** — it is the safety surface.

## Component naming (internal)

- The optional server/gateway may be named **Taita** (the house's provider/authority) — an internal component name, so
  external collisions don't matter.
- **Runa** works only as a combining morpheme if ever needed.

## Open decisions

- **Mobile feel:** PWA (keeps everything Go + Wails) vs a true native app (would pull the UI shell toward Tauri 2).
  Deferred — the thin-client architecture keeps it reversible.
- Web UI framework (lean; Svelte is a natural fit given llama.cpp's own webui and the small-footprint goal).

## Changelog

- 2026-10-27: Initial design captured — name chosen (**llaminGO**), architecture settled (Go core + web UI, thin shells,
  backend seam, storage-sync ≠ remote-access, native-API differentiator, client-owned MCP loop).

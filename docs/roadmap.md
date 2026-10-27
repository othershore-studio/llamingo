# llaminGO — Roadmap

Phased so nothing blocks on the hard parts. A real test suite and the testable-core / thin-shell split start at
increment #1.

## v1 — desktop + CLI + file sync (no server)

The whole thing, usable solo and on-LAN, with **no server to run or secure**.

- Go core: `Backend` seam (`LlamaCppNative`), chat API client, context/token logic (native `/props`, `/tokenize`, `/slots`).
- Client-owned **MCP loop** (`--jinja` tool-calls) — replaces the mcpo + front-end dependency.
- Store: plain files (per-conversation, per-project); BYO sync backend (local / WebDAV / SFTP / git); SQLite index only.
- Desktop shell (Wails) + CLI shell on the shared core.
- Image generation via an MCP (forge/ComfyUI) or native connection.

Proves the full product without a server. LAN mobile (home tablet/phone) also works here — it's just another LAN client.

## v2 — the gateway (remote + mobile)

The **optional** small server that earns its place only when you leave the LAN.

- Auth + reverse-proxy to llama.cpp + optional sync relay. Dockerable / `.service`.
- Serves the same web UI as a **PWA** → mobile without a native-app lift.
- This is the phase that justifies a server; v1 deliberately does not need one.

## Later — local tools (permission-gated)

- Local tool execution, **gated by a first-class permission model** designed in from the start (warnings, explicit
  grants). Build the gate before the tools.

## Non-goals

- Model downloading / training (assumes a running llama.cpp).
- Being a general "any backend" client (OpenAI-compatibility is a side effect, not a target).
- Replacing frontier models — the point is to make local good enough to carry the routine load and save frontier tokens.

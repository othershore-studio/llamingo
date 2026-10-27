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

## v2 — the gateway (remote + mobile + sync)

The **optional** small server that earns its place only when you leave the LAN.

- Auth + reverse-proxy to llama.cpp + **application-layer sync** of the `chat/` root (data only; each device rebuilds
  its own index — the DB is never on the wire). Dockerable / `.service`-friendly; bearer-token, auth-proxy posture, no
  OAuth inside.
- Serves the same web UI as a **PWA** → mobile without a native-app lift.
- The same HTTP/SSE adapter from v1, exposed and authenticated — a deploy mode, not a rewrite.
- *(Open: syncing the `chat/` root is the leaning; not finally committed.)*

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

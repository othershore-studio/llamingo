# CLAUDE.md — llaminGO

Operational guide for working in this repo. Design rationale lives in [`docs/`](docs/)
(`architecture.md`, `ui.md`, `roadmap.md`, `taita.md`); this file is the practical "how to work here."

## What this is

**llaminGO** — a sovereign, local-first chat client for **llama.cpp** (`llama-server`). A Go core with thin shells:
a CLI, an `internal/httpapi` HTTP/SSE adapter, a **Svelte** web UI (`web/`), and a **Wails** desktop app. It is a
*client*, not a runtime: it assumes a running `llama-server` and
leans on its **native API** (`/props`, `/apply-template` + `/tokenize`, `/slots`, streaming `/v1/chat/completions`
with `--jinja`) for honest context metering and a client-owned MCP loop. See `docs/architecture.md`.

## Module & toolchain

- Module path: `github.com/othershore-studio/llamingo` (AI tools live under **othershore-studio**).
- Go **1.24+**. Standard library first; few dependencies.
- MPL-2.0 header (Exhibit A) at the top of every `.go` file (copy from any neighbor). The `LICENSE` file is added at
  publication (see README).

## Layout

- `internal/backend/` — the one `Backend` seam to the engine; `LlamaCppNative` is the only implementation.
- `internal/lcpptest/` — a deterministic fake `llama-server` (httptest) for GPU-free tests.
- `internal/contextbudget/` — the single context builder + honest token budget.
- `internal/store/` — the two-root file store (free `chat/` + project `folder/`), per-conversation `meta.json`, memory.
- `internal/mcp/` — the client-owned MCP loop + JSON-RPC client (stdio / Streamable-HTTP) + config.
- `internal/httpapi/` — the HTTP + SSE adapter over the core (the surface the web UI consumes); agentic wiring + the permission handshake.
- `internal/permission/` — the scope × risk permission gate + grant store.
- `internal/imagegen/` — the native image tool (Forge Neo) + per-model recipes/config.
- `internal/memtool`, `internal/localtools`, `internal/filetools`, `internal/debuglog` — the other native tools + debug logging.
- `cmd/llamingo/` — the CLI shell; `cmd/llamingo-serve/` — the headless HTTP/SSE core (API only, **not** browser-facing — see Taita below).
- `internal/taita/` — the **v2 multi-user gateway**: a registry of per-session single-store `httpapi.Server`s (so `httpapi` itself stays untouched); `cmd/taita/` — the `taita` server binary (embeds the web UI, runs in server mode).
- `web/` — the Svelte 5 web UI (`web/src/App.svelte`), shared by the desktop webview and taita; `build/` — the Wails desktop app scaffolding.

## Taita (the v2 server) — the browser-surface invariant

**Taita** (`cmd/taita` + `internal/taita`) is llaminGO's optional multi-user gateway: a separate binary, grown from
`cmd/llamingo-serve` and sharing `internal/httpapi` + `internal/store`, that serves the web UI to a plain browser and
gives each session its own store. Full design + the phase plan: [`docs/taita.md`](docs/taita.md).

One invariant keeps drifting across compactions — **which surfaces a browser may open:**

- **Desktop (Wails)** — the only browser-facing *local* client. Its webview is Origin-gated (`httpapi.CORS`) so no
  stray page reaches the loopback core, which holds the backend token and the store.
- **`llamingo-serve`** — the **headless** loopback core, **API only**. Do **not** give it a web UI; that bypasses the
  CORS gate (a `-web` flag was tried and reverted, commit `cca6898`).
- **`taita`** — the browser-facing *server* surface (server mode); needs its own Origin gate before any public
  (tunnel) exposure.

## Build & test

```
go vet ./...
go test ./...          # GPU-free: everything runs against internal/lcpptest
go build ./...
go run ./cmd/llamingo -url http://localhost:8080 -m "Hello"   # CLI

npm --prefix web run build   # the Svelte web UI
wails build                  # the desktop app — close the running app first, or the exe is left stale/locked
```

## Conventions

- **Test-first, and through the fake.** New engine behavior gets a test that runs against `internal/lcpptest` —
  nothing merges without one (the Go analogue of the mock-transport rule used in the MCP servers). CI is `go vet` +
  `go test` on Linux: no model, no GPU.
- **One context builder.** Every model call (a turn, an MCP-loop step, a compaction summary) routes through
  `internal/contextbudget` so the budget never drifts.
- **Keep the homelab out of the repo.** The dev `llama-server` URL and any token come from the environment at run
  time (`LLAMINGO_URL`, `LLAMINGO_TOKEN`; the smoke test reads `LLAMINGO_SMOKE_URL`), never hardcoded — repos stay
  generic and public.

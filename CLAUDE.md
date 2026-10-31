# CLAUDE.md — llaminGO

Operational guide for working in this repo. Design rationale lives in [`docs/`](docs/)
(`architecture.md`, `ui.md`, `roadmap.md`); this file is the practical "how to work here."

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
- `cmd/llamingo/` — the CLI shell; `cmd/llamingo-serve/` — the HTTP server the desktop/web UI points at.
- `web/` — the Svelte 5 web UI (`web/src/App.svelte`); `build/` — the Wails desktop app scaffolding.

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

# CLAUDE.md — llaminGO

Operational guide for working in this repo. Design rationale lives in [`docs/`](docs/)
(`architecture.md`, `ui.md`, `roadmap.md`); this file is the practical "how to work here."

## What this is

**llaminGO** — a sovereign, local-first chat client for **llama.cpp** (`llama-server`). A Go core with thin shells
(CLI now; Wails desktop + web UI later). It is a *client*, not a runtime: it assumes a running `llama-server` and
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
- `cmd/llamingo/` — the CLI shell (consumes the core package directly).

## Build & test

```
go vet ./...
go test ./...          # GPU-free: everything runs against internal/lcpptest
go build ./...
go run ./cmd/llamingo -url http://localhost:8080 -m "Hello"
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

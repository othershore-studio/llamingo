# llaminGO — Architecture

Design notes for the client. The guiding principle is a **testable core + thin shells**: one core, with
UI / CLI / gateway as replaceable surfaces on top. This document records the decisions *and their rationale*,
so a later pass doesn't quietly undo a deliberate choice.

## Scope statement

- **Target backend:** llama.cpp (`llama-server`). Its **native API is the statement** — we use it where it beats
  the OpenAI veneer. This would only change if another engine became dominant *and* the whole stack moved with it.
- **Stays OpenAI-compatible** so it *can* point at other servers, but that is not a goal and will not drive design.
- **Primary shape is Claude-Code-like** — work anchored to a folder/project — with a free **chat** mode as a
  low-floor on-ramp. Both ship in v1.

## The core and its three faces

One **shared Go core** — the llama.cpp client, the MCP loop, the context/token logic, the store. It is exposed two
ways, and that distinction is load-bearing:

- **As an importable Go package** — consumed *directly* by the **CLI**.
- **As one HTTP + SSE/WebSocket adapter** over that package — consumed by the **web UI**.

The desktop shell (**[Wails](https://wails.io/)** — Go backend + system webview) points its webview at that HTTP
server on localhost. The v2 **gateway** exposes the *same* server behind auth. So the web UI speaks **one transport
everywhere** — identical in the desktop app and in the mobile PWA — and never uses Wails-specific bindings.

Why this matters:

- **Wails becomes reversible.** The desktop shell is "a window + a localhost server"; swapping the shell framework
  is a contained change, not a UI rewrite.
- **The v2 gateway is a deploy mode, not new code.** It is the same HTTP adapter, exposed and authenticated, rather
  than a second data layer built from scratch.
- **Streaming is in the spine from commit one.** SSE token streaming interleaves with both the live token budget and
  the MCP loop, so it is a core concern, not a late feature.

Terminal images in the CLI: capability-detect (Kitty / iTerm / sixel), else print a path/URL.

## Backend seam

All engine access goes through a single `Backend` interface, with **`LlamaCppNative` as the only implementation**.
This is not multi-backend support — it is one boundary so a future engine swap is a contained module change, not a
grep-through-the-codebase rewrite.

## Storage

Canonical state is **plain files you own**; any database is a rebuildable index on top. There is **no client-side
sync engine** (see *Sync is not a client concern*).

### Two roots, two sync destinies

Conversations divide by paradigm, and the split *is* the scoping axis used everywhere else (memory, permissions,
instructions):

```
~/.llamingo/
  memory.md                 # global memory (used by free chats)
  index.db                  # rebuildable cache — never the source of truth
  chat/<guid>/              # a free chat = one directory
    conversation.md
    meta.json               # title, created, model, …
    attachments/
  folder/<slug>-<hash8>/    # a PROJECT = a container of many sessions
    meta.json               # exact absolute path + stable folder-id + title
    memory.md               # project-scoped memory (personal; NOT in the repo)
    <session-guid>/
      conversation.md
      meta.json
      attachments/
```

- **`chat/`** — free-floating conversations, not tied to any folder. These are the **v2 sync / move-to-server
  candidates** (personal, portable).
- **`folder/`** — a project anchored to a working directory, Claude-Code-style, grouping many sessions.
  **Intrinsically machine-local**: the conversation references a filesystem path (and draws context from that folder's
  files and its `AGENTS.md`), which may not exist or may differ on another device. Remoting these is **v3/v4/never**,
  and the only coherent way it could ever work is to key by a stable id the *project itself* carries (e.g. a
  `.llamingo-id` beside `AGENTS.md`), not the local path — worth not precluding, not worth building.

### Keying & case-sensitivity

The `folder/` key is `<slug>-<hash8>`:

- **The slug is cosmetic and case-preserving** (`C--Code-llamingo`) — for humans reading the folder; it never decides
  identity.
- **Identity is a short hash of the canonicalized absolute path.** Canonicalization is the *one* place
  case-sensitivity enters: **case-fold before hashing only on a case-insensitive filesystem**, leave it exact on a
  case-sensitive one. So `/home/d/Code` and `/home/d/code` are two projects on Linux and one on Windows/macOS — both
  correct.
- Detect case-sensitivity from the **filesystem**, not `GOOS` (macOS can be case-sensitive; WSL/Linux vary) — probe it,
  with `GOOS` as a fallback heuristic.
- Also normalize separators, lexically clean `.`/`..`, and **NFC-normalize Unicode** (macOS NFD vs Linux NFC would
  otherwise fork an accented path). Do **not** chase symlinks by default (known edge case).
- Lookup ("do I already have this project?") is by hash, so the cosmetic slug may collide harmlessly.

### The index is rebuildable

`index.db` (SQLite, likely FTS for search) is a **cache**, never the source of truth. It is reconstructable by
scanning the `meta.json` files (each holds the exact path; recompute the hash to rebuild the hash→dir map). On startup
the core **reconciles index ↔ files** and self-heals — losing or corrupting the DB is a non-event, and a stale or
clobbered index (e.g. from an external sync tool) rebuilds from the canonical files. This is why SQLite must never be
the sync unit.

### Location

Everything lives under **`~/.llamingo`** (configurable via `--data-dir` / env). Index and data share the folder
because, with no client sync engine, nothing external is syncing it to corrupt — and self-heal covers the "user
deleted data, orphaned index" case regardless. If a user ever wants the sessions discoverable, `--data-dir` can point
at a clearly-named `~/Documents/llaminGO Sessions`.

### Memory & instructions — three layers that must not mix

| Layer | File(s) | Authored by | Model may write? | Lives where |
|---|---|---|---|---|
| **Instructions** | `AGENTS.md` (honor `CLAUDE.md` too) | human | **no** — read-only | *in the project repo* (committed, shared) |
| **Memory** (facts) | `memory.md` | human or model | **only via the gated write-tool** | `~/.llamingo/memory.md` (global) · `folder/<key>/memory.md` (project; personal, not in repo) |
| **Tool-coaching** | — | llaminGO | n/a | llaminGO's own system-prompt assembly |

Keeping these separate is a **safety** property, not tidiness: the write-tool's target is exactly one file, so
instructions can't be clobbered by a model; and instructions (shared, committed) have a different lifecycle from
memory (personal, local). `AGENTS.md` is the nearest cross-tool convention; `CLAUDE.md` is widespread — honor both as
read-only directives.

### Sync is not a client concern

Earlier drafts had the client do "bring-your-own-backend" file sync. **Dropped.** A folder of plain files already gets
Syncthing / rclone / OneDrive / git for free if the *user* points their own tool at it (unsupported, at their own
risk; the index self-heals). Building and testing a sync engine inside the client is scope we don't need.

- **v1 is single-machine.** Desktop + CLI share one local `~/.llamingo`. (This revises the earlier promise of in-v1
  cross-device "continue on mobile" — that is now a v2 capability.)
- **v2's gateway is the *supported* multi-device answer**, syncing at the **application layer**: it transfers
  conversation *files* (the `chat/` root) and each device **rebuilds its own index locally**. The index is never on
  the wire — that is how "SQLite is never the sync unit" is enforced by protocol, not by folder tricks.
  Last-writer-wins per file is acceptable (file-per-conversation makes real conflicts rare and human-resolvable; no
  CRDT).

## Context management — honest metering + non-destructive action

The headline feature, and the clearest payoff of the native API.

### Metering

Two numbers, never conflated:

- **Authoritative total** — comes back on every response as `usage.prompt_tokens`. Ground truth, post-hoc.
- **Pre-flight estimate** — render the prompt yourself via `POST /apply-template`, then `/tokenize` the result. Exact
  pre-send count, and it correctly includes the (often large) tool-definition cost, since `--jinja` renders tool
  schemas into the prompt.

The **segment breakdown** (system · instructions(AGENTS) · memory · tools · conversation) is computed incrementally
(tokenize cumulatively, attribute each segment to the delta); whatever the template adds on top gets its own honest
**"format overhead"** line rather than being smeared across segments.

There is **exactly one context builder** in the core, and *every* model call routes through it — a normal turn, each
MCP-loop step, and the compaction-summary generation itself — or the budget math drifts.

Set `llama-server`'s **context-shift behavior explicitly** (client in control) so the number shown is the number that
is real; a silent server-side KV shift would make the "truthful budget" lie. `/slots` is an *enhancement* (it can be
disabled server-side and can expose prompt content), not a hard dependency — `/props` + `/tokenize` are the reliable
pair.

### Actions

- **Estimate** current context (the breakdown above).
- **Compact** on command (a Claude-like `/compact`).
- **Auto-compact** when a threshold is crossed.
- **Report** when a compaction happened (and how much it saved).
- **Monitor** the server and warn.

### Compaction is a projection, never a deletion

> The file on disk is the full truth; the prompt sent to the model is a *projection* of it.

- Messages are **never deleted or rewritten.**
- A compaction appends a **summary block** — itself readable Markdown — whose frontmatter records the id-range it
  covers, the model/time that produced it, tokens-replaced and tokens-of-summary.
- The context builder **substitutes** the active summary for its covered range when assembling the prompt. The store
  is untouched.

This gives, at once: full history in the UI (compacted spans shown collapsed with a "summarized" badge), concrete
reporting ("compaction saved 7.2k tokens"), reversibility (re-compact / un-compact), and intact sovereignty (someone
opening the `.md` sees the real messages *and* a plainly-labeled summary of what was condensed). Data model: a single
linear log where a summary is just another typed entry that **shadows** a range — not full context-state snapshots.

Note: **model-switch is a budget event.** `n_ctx` and the tokenizer are per-model, so switching re-tokenizes against
the new model and recomputes the budget; old counts don't transfer.

## Why llama.cpp *native* (the differentiator)

A generic OpenAI-API client can't do these; a llama.cpp-focused one can:

- `/props` → real context size and the chat template; `/apply-template` + `/tokenize` → **exact** token counts;
  `/slots` → KV-cache state (when enabled). Together: honest, accurate context management — a visible, truthful
  budget, not a hidden window.
- `--jinja` → the server parses the model's chat template and emits OpenAI-style `tool_calls`.
- **GBNF / JSON-schema constrained decoding** → force valid output, including a valid tool call out of a
  willing-but-sloppy small model. This is the reliability floor nothing else gives you.

## MCP — the client owns the loop

With `--jinja` emitting `tool_calls`, the client runs the full loop itself:

> client ↔ MCP (stdio/HTTP) to its own servers · client ↔ chat API to `llama-server` · get `tool_calls` · execute the
> MCP tool · feed the result back · repeat.

This removes any dependency on an OpenAPI bridge (mcpo) or a separate chat front-end for tool use.

## Tools, image generation & the permission gate

### Native tools vs MCP tools

A tool is surfaced to the model the same way regardless of who runs it (via `--jinja` tool-calls); what differs is
**execution**. **MCP tools** dispatch to an MCP server; **native tools** are implemented in-process by the core.
Native tools are what the roadmap files under "Later: local tools" — and image generation pulls a thin slice of that
forward into v1 as the **first native tool**.

### Image generation

Exposed as a **built-in native tool** (agentic), *not* requiring any MCP — because most users have forge/ComfyUI but
not a bespoke image MCP. Behind it sits an **`ImageGen` seam** (direct-forge / direct-ComfyUI / optional
MCP-passthrough), parallel to the `Backend` seam.

- Results are saved to the conversation's `attachments/` and **linked** in chat; the model gets an **out-of-band ref,
  never base64** (keeps bulk bytes out of context).
- The tool exposes **structured params** (size, steps, sampler, seed, negative) for control and reproducibility.
- Prompts are passed **verbatim** — the "smart" part is tool ergonomics (good params, a description a 12B gets right),
  never hidden prompt-rewriting.

### The permission gate

Built first, proven by image gen. Two axes:

- **Grant scope:** per-invocation / per-session (this chat) / persistent.
- **Per-tool risk policy** decides which scopes are even offered and how much friction rides along.

Risk defaults are driven by the tool's declared **MCP `ToolAnnotations` hints** (`readOnly` / `destructive` /
`idempotent` / `openWorld`) — a read-only tool is cheap to grant for-this-chat; a destructive one defaults to
per-invocation with a *specific* confirmation ("run this Python: …"). Hints are **advisory — they set defaults and
friction, never enforcement** (sandboxing a tool is a separate concern from asking about it).

Philosophy: the user has all the power (they can always escalate their own trust), but a dangerous escalation costs a
deliberate act — friction is *informational*, a chance to second-guess, not paternalism. Grants persist in a plain
file; session grants live in memory and die with the chat. A grant **binds to a stable tool identity** (server + name
+ a hash of the definition); a changed definition re-prompts (confused-deputy guard).

v1 surface is tiny: image gen is low-risk → `allow this chat / always / ask each time`. The machinery is designed now;
future tiers (Python, shell, filesystem) slot in by declaring higher risk.

## Model & capability awareness

### Model selection

Users may run several models. Support **(endpoint × model) pairs** — plain `llama-server` is often one-model-per-
process, so "pick a model" is frequently "pick which server," while `/v1/models` (or a swapper like `llama-swap`) can
expose several behind one endpoint. The chosen model is stored per-conversation in `meta.json`. (Switching
mid-conversation is a budget event — see *Context management*.)

### What's standard, and what isn't

The request crosses three boundaries with three different answers, and this is *why* capability-awareness is
unavoidable:

- **Client ↔ tool servers:** a real open standard — **MCP**.
- **Client ↔ model API:** a strong *de-facto* standard — OpenAI Chat Completions + **JSON Schema** tool params; plus
  the model's **Jinja `chat_template`** shipped in the GGUF (mechanism standardized, content per-model).
- **The model's actual tool-calling behavior:** **not standardized** — each family formats and emits tool calls
  differently; llama.cpp carries per-family parsers; small or low-quant models often emit *almost*-valid syntax that
  doesn't parse.

Targeting llama.cpp specifically **collapses the problem from two axes (server × model) to one (model)**, and `--jinja`
+ the parsers + GBNF absorb most of that remaining axis.

### Capability profile — a principle, not yet a spec

llaminGO must **probe and be honest** rather than assume a model does tools / memory / thinking well. Per-model, it can
detect template presence, `n_ctx`, tool-call reliability and the thinking mechanism, then **adapt** (e.g. auto-engage
GBNF forcing for a sloppy model). Guiding stance: **design to the floor** — small models are the design target,
big-model autonomy is additive; the profile *grants* capability as a model earns it rather than degrading from an
assumed ceiling. Four variables decide tool-use in-stack — model competence × GGUF template correctness × llama.cpp
parser coverage × **quant** — so evals **pin the quant**, not just the model.

### Effort / "thinking"

The *least* standardized feature in the stack — it varies on the model **and** the `llama-server` launch config. It
appears as always-on text (`<think>…</think>` in R1/distills), a binary toggle (Qwen3 `enable_thinking`), or graded
effort (`reasoning_effort: low|medium|high` in gpt-oss), and whether it parses depends on `--jinja`,
`--reasoning-format`, and the GGUF's template. Stance:

1. **Honest control shaped to the model+config** — low/med/high, or a toggle, or nothing; never a fake uniform slider.
2. **Lean on llama.cpp's own model-awareness first** (it parses templates and can surface `reasoning_content`), so
   "keep the app updated" is mostly "keep llama.cpp updated," ridden for free.
3. **A thin, updatable supplement as *data, not code*** for the gaps the server won't expose — shippable independently
   of the binary, with a user override.
4. **Defensive parsing always** — detect `<think>` / `reasoning_content` and render a collapsible thinking block even
   for an unknown model.

## Testing

Two kinds, split by infrastructure (the real test suite starts at increment #1):

- **Core-logic tests** — against a **mocked `llama-server`** (recorded `/props`, `/tokenize`, streamed-completion
  fixtures): deterministic, GPU-free, CI-friendly. Cover the context math, the MCP / native-tool plumbing, the store
  and keying.
- **Model-capability probes** — against a **real local model** (a 4B fits this laptop; up to ~30B available): not CI,
  scored over trials because nondeterministic. "Does this model honor memory / call `save_memory` / emit parseable
  `tool_calls`?" This doubles as the loop for tuning the tool-coaching layer.

## Component naming (internal)

- The optional server/gateway may be named **Taita** (the house's provider/authority) — an internal component name, so
  external collisions don't matter.
- **Runa** works only as a combining morpheme if ever needed.

## Open decisions

- **v2 chat-sync:** the gateway syncing the `chat/` root is the *leaning*, not finally committed.
- **Mobile feel:** PWA (keeps everything Go + Wails) vs a true native app (would pull the UI shell toward Tauri 2).
  Deferred — the thin-client architecture keeps it reversible.
- **Web UI framework:** lean toward Svelte (fits llama.cpp's own webui and the small-footprint goal); not settled.
- **UX design** is the next phase — reference point: the *margin* app's design language.

## Changelog

- 2026-10-27: Initial design captured — name chosen (**llaminGO**), Go core + web UI, thin shells, backend seam,
  native-API differentiator, client-owned MCP loop.
- 2026-10-27: Design settled in depth — core-as-package + one HTTP/SSE adapter (CLI direct); **sync reversed** (no
  client sync engine; v1 single-device; cross-device → v2 gateway, app-layer, data-only); two-root file store
  (`folder/` project vs `chat/` free) with slug+hash keying and FS-aware case handling; honest context metering
  (`/apply-template` + `/tokenize`, segment breakdown + overhead line, one context builder) and **non-destructive
  compaction** (projection, not deletion); image generation as the first **native tool** behind an `ImageGen` seam;
  **permission gate** (scope × risk, MCP-hint-driven defaults) proven by image gen; three-layer **memory/instructions**
  split (AGENTS.md in-repo read-only · memory.md personal, gated-write · tool-coaching in-app); **capability-awareness**
  (incl. model selection and effort/thinking) kept as a principle, leaning on llama.cpp; two-tier testing (mocked core
  vs real-model probes).

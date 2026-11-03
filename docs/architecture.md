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
server on localhost; the **Android app** does the same, with the core **bound in-process via gomobile**. The v2
**gateway** exposes the *same* server behind auth. So the web UI speaks **one transport everywhere** — desktop,
Android, and later the gateway — and never uses Wails-specific bindings.

Why this matters:

- **Wails becomes reversible.** The desktop shell is "a window + a localhost server"; swapping the shell framework
  is a contained change, not a UI rewrite.
- **The v2 gateway is a deploy mode, not new code.** It is the same HTTP adapter, exposed and authenticated, rather
  than a second data layer built from scratch.
- **Streaming is in the spine from commit one.** SSE token streaming interleaves with both the live token budget and
  the MCP loop, so it is a core concern, not a late feature.

Terminal images in the CLI are **shipped**: generated images render inline in the transcript (half-block
cells — fg = top pixel, bg = bottom — double the vertical resolution), plus an `/image` full viewer —
truecolour colour-blocks, or sixel where the terminal advertises it. Colour depth follows the terminal
(`COLORTERM=truecolor` over SSH).

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

### Memory integrity — tamper-evidence

Memory is the design's prime injection surface — it is injected into context, the model can write it (gated), and it
rarely changes — so an unexpected change is a strong signal (tested: an external edit to `memory.md` does alter model
behaviour). llaminGO keeps a **hash of each `memory.md`** in the local SQLite db, recorded after every edit through a
known path: the UI editor (user) or the gated write-tool (model). On load, a hash mismatch means memory changed
*outside* those paths.

- **Mismatch is a review alert, never a block.** The files are the user's to edit in any tool, so a mismatch surfaces
  a calm *"memory changed outside llaminGO — review and accept?"*; accepting stores the new hash. The user owns their
  data, so an alert is enough for v1.
- **Tamper-evidence, not tamper-proofing.** A local actor who can write `memory.md` can usually write the db too, so
  this is a tripwire against accidental, external, or injected edits — not a guarantee. The hardening path is to key
  the hash with a secret (a per-install seed / HMAC in the OS keystore), so defeating it needs the keystore secret,
  not just file+db write. Hash *normalized* content (LF, trimmed) so a line-ending rewrite is not a false positive.
- **This is the db's one piece of non-rebuildable trust state** — unlike the index (a rebuildable cache), losing it
  leaves the app able only to re-trust whatever is on disk. It lands with memory and SQLite in increment #6.

**Conversation hashing is deliberately out of scope.** A transcript changes every turn, so a whole-file hash is
noise; and a conversation's real injection vector is the *content the model reads* (tool results, web pages,
`@`-mentioned files), defended by the instruction-source boundary and the permission gate, not by hashing the
transcript. v1 is fully local, too — without the v2 server there is no authority to flag a conversation as
suspicious. If conversation integrity is ever wanted, the shape is a hash-chain over the append-only log (enforcing
non-destructive compaction's append-only history), deferred to v2+.

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

### Configuration, trust & the frontier hand-off

- **Claude-compatible config, two scopes:** a **global** `~/.llamingo/mcp.json` (your servers, everywhere) and a
  **per-project** `.mcp.json` in the **working folder** (the repo), using Claude Code's `mcpServers` schema (stdio
  `command`/`args`/`env`, or remote `type`/`url`). Both are plain files you own; the Tools UI (see [`ui.md`](ui.md))
  is a convenience over them, never a second source of truth. `.mcp.json` sits in the repo beside `AGENTS.md` — a
  project's servers are shared "how to work here," not personal.
- **Why compatibility is strategic, not just convenient.** Local has a ceiling; for a complex task the user may
  escalate to a frontier model. Because a project carries a Claude-shaped `.mcp.json` *and* `AGENTS.md` in its repo,
  it **opens in Claude Code / Claude Desktop as-is** — servers and instructions intact. That is the README thesis made
  operational: local for the routine load, a **frictionless hand-off to the frontier** when a task needs it. llaminGO
  is the local tier of a continuum, not a walled garden; the project folder is the portable unit that rides up. Claude
  is the de-facto standard and the v1 target (`AGENTS.md` is already cross-tool).
- **Trust follows the source.** A committed `.mcp.json` is *data from the repo*, and an MCP server is arbitrary
  execution — so **project servers clear the permission gate before first run**, while **global servers are trusted**
  (you authored them). Same instruction-source-boundary logic as everywhere else: the repo is not a standing grant.

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

### write_memory — the second native tool

Memory's **read** path is always-on (injected as a metered segment; see *Memory & instructions*). The **write** path
is a native tool, `write_memory`, so the model can propose durable updates — gated at **medium risk** (a side effect),
so the gate offers only *this-chat* or *once*, never a standing "always" (the model cannot silently rewrite memory on a
blanket grant). The model already sees current memory in context, so it composes the **complete** new version; the tool
writes it verbatim and re-hashes (clearing the tamper flag). The consent prompt shows the exact proposed content.

### The permission handshake

The gate asks the user over the **one-way SSE chat stream**, without breaking the thin-client seam. When a tool clears
no standing grant, the gate's `Prompter` — reached synchronously deep in the tool loop, on the request goroutine —
emits a **`permission` event** (`{id, label, detail, risk, scopes}`) on that stream and **blocks**. The answer returns
out-of-band on **`POST /api/permission/{id}`** `{allow, scope}`; a small in-process **broker** (`id → channel`) routes
it back to the waiting call, which records the granted scope and proceeds. Silence times out to a deny; a dropped
connection is reaped by the request context. One app-wide prompter + broker serve every in-flight chat (the stream is
found in the request context), while **gates are per-conversation** so a *this-chat* grant never leaks across chats.

This is also what wires the **client-owned MCP loop** (`internal/mcp.Run`) into the live chat: the stream drives the
loop, mapping its deltas to `reasoning` / `delta` / `tool_call` SSE events and persisting only the **final** assistant
answer (tool-call turns are live-streamed scratch, like reasoning — a tool's durable effect is its own artifact). Image
generation and project MCP servers join the same registry (`mcp.Combine`) behind the same gate.

## Model & capability awareness

### Model selection

Users may run several models. Support **(endpoint × model) pairs** — plain `llama-server` is often one-model-per-
process, so "pick a model" is frequently "pick which server," while `/v1/models` (or a swapper like `llama-swap`) can
expose several behind one endpoint. The chosen model is stored per-conversation in `meta.json`. (Switching
mid-conversation is a budget event — see *Context management*.)

**v1 status:** one connection, one model — v1 shows the connected model (from `/props`) and stores it per-conversation;
a model/connection *picker* is a v2 (**Taita**) concern, where the gateway can expose several named connections —
including same-model "personalities" with different system instructions, at no VRAM cost — rather than the client
juggling servers.

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

The trace is **persisted** as a display-only `reasoning` log entry (a sibling written just before the answer it belongs
to), shown when a chat is reopened. It is kept **out of the prompt** — `store.Project` includes only messages and
summaries — so storing the thinking costs no context budget and does not change when auto-compaction fires; the only
cost is a larger log file on disk. Always-on for now; an opt-out is a later nicety.

**Thinking control** — the user can turn a model's thinking on or off **per turn**. llaminGO offers the toggle
only when the model's chat template exposes `enable_thinking` (a template-string check on the `/props`
`chat_template`; a render-diff probe against `/apply-template` is a stronger upgrade), and drives it with
llama.cpp's `chat_template_kwargs` — a **server extension the OpenAI standard does not define**, so a generic
client (OpenWebUI) can't send it and is reduced to a system-prompt `<|think|>` hack that can't be turned off per
request. Omitted when unset, so the server's own default stands. This is a concrete instance of *why* llaminGO is
llama.cpp-native rather than "any OpenAI backend".

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
- **Mobile feel:** *resolved* — a native **Android app** (a `gomobile`-bound Go core in a WebView serving the shared
  UI), neither a PWA nor Tauri. The thin-client architecture kept the choice reversible, exactly as intended.
- **Web UI framework:** *resolved* — **Svelte** (fits llama.cpp's own webui and the small-footprint goal).
- **UX & interface:** captured in [`ui.md`](ui.md).
- **"Compatible with" other clients (v2):** survey other popular clients' project/standard formats and a per-project *compatible-with* setting; Claude is the v1 target.

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
- 2026-10-27: UX first pass captured in [`ui.md`](ui.md) — margin-derived calm sensibility, two-panel shell, distributed context budget, own Andean-warm identity.
- 2026-10-27: MCP config settled — Claude-compatible global `~/.llamingo/mcp.json` + per-project repo `.mcp.json`; project servers gated, global trusted; the frontier hand-off rationale; a scope-aware Tools surface (see [`ui.md`](ui.md)); "compatible with" other clients deferred to v2.
- 2026-10-27: Memory integrity settled — a hash of each `memory.md` in the local SQLite (the db's one non-rebuildable trust-state), review-on-mismatch (never a block), with a keystore-keyed HMAC as the hardening path; conversation hashing ruled out (churn + wrong vector). An increment-#6 feature.
- 2026-10-28: Agentic capstone — the client-owned tool loop (`mcp.Run`) wired into the chat stream, with the **permission handshake** over SSE (a `permission` event + out-of-band `POST /api/permission/{id}`, correlated by an in-process broker; per-conversation gates). `write_memory` lands as the **second native tool** (gated, medium-risk) and the proving customer; image gen + MCP servers join the same `mcp.Combine` registry. Tool-call turns stream live but only the final answer persists (faithful tool-trail persistence deferred to a log-schema change).
- 2026-10-28: Reasoning trace persisted — the model's thinking is stored as a display-only `reasoning` log entry before each answer (its own HTML-comment marker), shown on reload. `store.Project` keeps it out of the prompt, so it costs no budget and leaves compaction unchanged; the only cost is a bigger log. Always-on (an opt-out toggle is a later nicety).
- 2026-10-28: Thinking toggle — a per-turn thinking control wired to llama.cpp's `chat_template_kwargs.enable_thinking` (a field on `ChatRequest`, threaded through `mcp.Run`), offered in the UI only when the model's template exposes `enable_thinking` (detected from the `/props` `chat_template`). Honest, per-request control the OpenAI-standard surface can't express; omitted when unset so the server default stands. The render-diff probe against `/apply-template` is the noted v2 upgrade.
- 2026-10-28: tokens/sec + timestamps — the backend parses llama.cpp's `timings` on the terminal chunk (`Delta.Timings`); the chat stream's `done` event carries the generation rate, shown as `tok/s` in the status bar beside the budget. Each message also gets a numeric ISO-ish local timestamp below it (`2026-10-28 15:42`, not a day name), from the stored entry time. Honest metering extended from context to speed.
- 2026-10-30/31: **Image generation shipped end to end** — a native **Forge Neo** `txt2img` tool behind the `ImageGen` seam (no MCP needed); **per-model recipes** (name-inferred from the checkpoint + a Settings editor for steps/CFG/sampler/scheduler/size/modules, stored as deltas from the default); an **auto-pick allow-list + sticky-safe default** (shared-GPU guard: a heavy modern DiT is opt-in, so it can't OOM `llama-server` mid-turn); **prepopulate** (seed the model list from Forge's `/sdapi/v1/sd-models`); one-at-a-time serialization; per-chat **debug logging** of raw LLAMA/FORGE/TOOL payloads; a shaping round + image-recovery (the model writes the caption, we attach the picture); and MCP Streamable-HTTP **session recovery**. **Graceful backend-death**: `ChatStream` runs an idle-timeout, so a silently-dead backend ends the turn with an error instead of hanging.
- 2026-10-31: **Tool configuration** — **per-chat curation** (each chat's `DisabledTools` in its `meta.json`; a Tools panel grouping **System** vs **MCP** tools, applied fresh each turn via `mcp.Curate`); **MCP server config from the UI** (Settings → MCP servers: add/edit streamable-HTTP servers — name/URL/auth header — with **live reconnect**, no restart, replacing the connect-once cache); **project MCP** (a project's own `.mcp.json` wired per session, connected per project and **gated**, since project servers aren't user-authored); and **per-project system-tool defaults** in a **`.llamingo/project.json`** in the working folder (mirroring `.claude/`; a new session inherits them, still overridable per chat). Three surfaces — **Settings** (global) / **Tools** (per-chat) / **Project** (per-folder) — *configuring* kept separate from *activating*.
- 2026-11-01: **Connection personas** — a saved connection carries not just URL/token but **temperature + min_p + a
  system prompt**, so two connections on the *same* `llama-server` are distinct personas (a low-temp "Coder", a
  high-temp "Creative") at no VRAM cost — v2-Taita's "named connections = personalities" pulled into v1. Only temp +
  min_p are exposed, not the exotic samplers; model-*within-a-server* selection stays a v2 concern.
- 2026-11-01: **File coding tools** — `read_file` + gated `write_file` (read-before-overwrite), then a fuller set
  (`folder_tree`, `rename`/`create`/`copy`/`delete` file+folder, `file_properties`) in `internal/filetools`,
  **project-confined** (`resolveWithin` + symlink/`..`/absolute rejection) and **risk-tiered** on the existing gate
  (reads ungated, writes/renames/copy gated, deletes always-ask). A project session is also told its working
  directory. Deliberately minimal — "local = light tasks and code."
- 2026-11-01: **Loopback hardening** — the desktop API is **Origin-allowlisted** (`httpapi.CORS`, not `ACAO:*`),
  closing a browser drive-by that could read the backend token via `GET /api/settings/connections`. Chosen over a
  per-launch token (the webview origins are verified from Wails' own source).
- 2026-11-01: **Android app (Option A)** — the **same Go core + Svelte UI on a phone**. A normal Android app can't
  `exec` a shipped binary (API-29+ W^X), so the core runs **in-process** via a **gomobile**-bound `.aar` that serves
  the API *and* the `//go:embed`'d SPA on a loopback port, behind a `WebView`. No rewrite — the UI↔core HTTP boundary
  (one `apiOrigin()` chokepoint) made it drop in. `targetSdk 34` sidesteps Android 15/16 forced edge-to-edge for now;
  iOS rides the same path (`bind -target=ios`). Mobile = desktop minus Projects.
- 2026-11-01: **Open-sourced + CI/releases** — the repo went **public** (MPL-2.0 LICENSE/NOTICE + CONTRIBUTING +
  SECURITY, branch protection, private vulnerability reporting). CI runs `go vet`/`go test` + web and Windows builds;
  **tag-driven workflows** publish a signed Android APK and a Windows build to a GitHub Release, the signing gated
  behind a required-reviewer Environment. **v0.1.0** is the first public release.

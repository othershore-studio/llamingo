# llaminGO — User guide

How to actually use llaminGO day to day. It assumes you already have a
**`llama-server`** (from [llama.cpp](https://github.com/ggml-org/llama.cpp)) running
somewhere you can reach — llaminGO is a *client*, not a runtime. For the design
rationale behind any of this, see [`architecture.md`](architecture.md); for the
terminal client specifically, [`cli.md`](cli.md).

- [The surfaces](#the-surfaces)
- [Connecting to a model](#connecting-to-a-model)
- [Connections and personas](#connections-and-personas)
- [The terminal client](#the-terminal-client)
- [The context budget](#the-context-budget)
- [Tools](#tools)
- [The permission gate](#the-permission-gate)
- [Projects](#projects)
- [Memory](#memory)
- [Image generation](#image-generation)
- [Where your data lives](#where-your-data-lives)

## The surfaces

llaminGO is one Go core behind a few thin shells. They share the same store, so a
chat you start in one shows up in the others:

- **Desktop** — a Wails app (Windows today; the shell is portable). The full UI:
  connections, settings, the tools panel, projects, image models.
- **Terminal (TUI)** — the `llamingo` binary. Run it in any folder and it opens a
  session anchored to that folder's project, over the same core, with no server.
  This is the home for Termux, SSH boxes, and anyone who lives in a terminal.
- **Web** — the same Svelte UI the desktop embeds, served by `llamingo-serve` for a
  browser.
- **Mobile** — an experimental Android app (the Go core via gomobile in a WebView),
  distributed through GitHub Releases and sideloaded.

Everything below is phrased for the desktop and the terminal; the web UI mirrors the
desktop.

## Connecting to a model

llaminGO talks to a `llama-server` over its native API. Start one with tool use and
honest templating enabled — the key flag is **`--jinja`**:

```bash
llama-server -m your-model.gguf --jinja --port 8080
```

Point llaminGO at it:

- **Desktop / web** — add a connection in Settings (below).
- **Terminal** — `llamingo` uses your active saved connection by default; or override
  with `-url` / the `LLAMINGO_URL` environment variable:

```bash
llamingo -url http://localhost:8080
```

If a server isn't reachable, llaminGO tells you plainly rather than hanging.

## Connections and personas

A **connection** is a saved `llama-server`: a name, its URL, an optional bearer token,
and — this is the trick — its **sampling** (temperature, min-p) and an optional
**system prompt**. Because those travel with the connection, two connections pointing
at the *same* URL become two **personas**: a low-temperature **"Coder"** and a
high-temperature **"Creative"** are different entries you switch between, even though
one model is running.

- **Add / edit connections** in the desktop Settings. One is *active* at a time; that
  is the one new chats use.
- **In the terminal**, `/model` (alias `/connection`) opens a picker of your saved
  connections and switches the current session to one — its backend, sampling, and
  persona — for the rest of the run. (It doesn't change which connection is globally
  active; that stays the desktop's to set.)

One `llama-server` serves one model, so switching the connection is how you switch
model.

## The terminal client

Run `llamingo` in a project folder. It resolves (or creates) that folder's project,
loads its config, connects, and drops you into a chat. The status bar shows the
project, the connection alias, and the live context budget.

**Typing**

- **Enter** sends. **Ctrl+J** inserts a newline (Shift/Alt+Enter too, where your
  terminal distinguishes it). The composer grows as you type.
- **PgUp / PgDn** scroll the transcript. **Ctrl+C** quits.
- **Mouse is off by default**, so your terminal's own select-and-copy works. `/mouse`
  turns on wheel-scroll (then selection is Shift+drag).

**Commands** (type them in the composer)

| command | what it does |
|---|---|
| `/new` | start a fresh session in this project |
| `/resume` | open a past session (a picker, newest first) |
| `/model` | switch connection / model for this session |
| `/tools` | turn this chat's tools on and off (System and MCP) |
| `/memory` | show what's in saved memory |
| `/prompt` | show the system prompt the model is actually given |
| `/thought` | read the model's thinking for the last turn |
| `/image` | view generated images full-screen (←/→ to browse) |
| `/mouse` | toggle wheel-scroll (off = native select & copy) |
| `/clear` | clear the view |
| `/help` | list the commands |
| `/exit` | quit |

**Resuming** — `llamingo -c` reopens this project's most recent session on launch;
`/resume` picks any past one. Opening the TUI without sending a message leaves nothing
behind (sessions are created on your first message).

**Images** render inline in the chat as colored blocks and open crisp with `/image`;
they also show in the desktop, since the reference is saved into the reply.

## The context budget

Every model call is metered honestly against the server's real context window — not a
guessed window. The status bar shows `ctx used/total`, counting the conversation, the
system framing, **and the tool schemas** (those are a real chunk of the prompt). It is
coloured as it fills: **green** under 75%, **amber** approaching, **red** near full.

When a conversation outgrows the window, it **compacts**: older turns fold into a
summary that stands in for them, so the thread continues without silently dropping
context. The pre-turn figure is an estimate; the authoritative count folds in from the
server's own usage after a turn runs.

## Tools

llaminGO runs the tool-call loop itself (the "client-owned MCP loop"), so tools work
the same across models. Three kinds:

- **System tools** — built in: memory, the current time and your location, and the
  project **file tools** (read, write, rename, copy, create, delete, folder tree,
  properties — all confined to the project folder).
- **MCP servers** — any server you configure, global or per-project (see
  [Projects](#projects)); the client connects them and offers their tools.
- **Image generation** — a native tool when a Forge server is configured (see
  [Image generation](#image-generation)).

**Curation.** A small local model drowns in too many tools — native calls degrade into
confabulated text once the set grows past a handful. So the toolset is **curated per
chat**: in the desktop Tools panel, or `/tools` in the terminal, toggle individual
tools off. The terminal list labels each one `system` or `mcp · <server>` and persists
your choice for that chat. A project can also set which System tools a new chat starts
with (`.llamingo/project.json`).

## The permission gate

Tools that *do* something — write a file, generate an image, a project MCP server's
first call — pass through a **permission gate** before they run. You are asked, with
the specific action named, and you choose a scope:

- **Once** — just this call.
- **This chat** — for the rest of the conversation.
- **Always** — remembered persistently.

Read-only tools (reading a file, the time) don't prompt. On the desktop the prompt is
an inline handshake; in the terminal it's a modal — same scopes, same friction. The
gate is the one safety layer, so adding a tool is cheap: wrap a function, declare its
risk tier, and the gate does the rest.

## Projects

Run llaminGO in a folder and that folder is a **project** — its chats, its tools, its
memory, scoped to it. A project is just the folder plus a few optional files it reads:

- **`AGENTS.md`** (or `CLAUDE.md`) — project instructions, prepended to the model's
  context. Also, the model is told its working directory so it uses relative paths.
- **`.mcp.json`** — MCP servers for *this* project (the same file format other tools
  use). Their tools are gated per project — consent on first use, shared across that
  project's chats.
- **`.llamingo/project.json`** — the System tools a new chat in this project starts
  with (you can still override per chat).

The same folder opens to the identical project in the desktop and the terminal:
configure once, use from either. Chats that aren't tied to a folder are "free chats",
kept separately.

## Memory

llaminGO keeps a small **personal memory** the model can write to (the `write_memory`
tool) and that is fed back into every turn, so preferences and facts persist across
chats. It's a plain file you can read and edit yourself; `/memory` shows it in the
terminal, and the desktop has a memory view. If it's edited outside the app, llaminGO
notices and tells you.

## Image generation

With a **Forge** (Forge Neo) image server configured, llaminGO offers a native
`generate_image` tool — zero end-user setup beyond pointing at the server. Configure
the Forge URL (Settings, or `LLAMINGO_FORGE_URL`) and your image models
(`imagegen.json`, with per-model recipes). Generated images save into the chat's
attachments, render inline, and open full-screen with `/image` in the terminal. Image
generation is **off** until both a URL and at least one model are configured.

> On a shared GPU, generating an image loads VRAM and can disrupt a resident
> `llama-server` mid-turn; llaminGO rides this out with a stream idle-timeout rather
> than hanging. A small always-resident model alongside Forge is the smoother setup.

## Where your data lives

Everything is **plain files under `~/.llamingo`** — config, connections, chats,
projects, memory, image config. No database, no cloud. Back it up or point your own
sync tool at it. The terminal binary needs no installer and writes only to your user
context; `-data-dir` or `LLAMINGO_DATA_DIR` moves the root if you want it elsewhere.

On Windows, **`llamingo -setup`** optionally adds the binary's folder to your PATH (so
`llamingo` runs from anywhere) and an Explorer right-click menu on folders — *"Call a
llaminGO here"* and a resume variant. It's your user context only (no admin) and
reversible; re-run it if you move the binary and it will repoint itself. On **Windows 11**
the entry lives in the *classic* context menu, so use **Shift+right-click** (or "Show more
options") to see it — registry menu entries don't appear in the new compact menu.

# llaminGO

A sovereign, local-first chat client for **[llama.cpp](https://github.com/ggml-org/llama.cpp)** users — in the
spirit of a desktop/mobile AI client, but for the models *you* run, on hardware *you* own.

> **Status:** design / planning (pre-code). This repo currently holds `.md` design docs only.

## What it is

A **client**, not a runtime. It assumes you already have `llama-server` running and makes it pleasant and powerful
to actually use — with real context management, chat history, MCP tools, image generation, and projects.

- **Context management** backed by llama.cpp's *native* API (true token metering, not a hidden window).
- **Chat history & projects** as plain, portable files you own and can sync yourself.
- **MCP** as a first-class citizen — the client runs the tool-call loop itself.
- **Image generation** as a built-in tool (direct to forge/ComfyUI; an image-gen MCP optional).
- **Desktop first**, with mobile and remote access as deliberate later phases.

## What it is *not*

- Not a model downloader or trainer — it connects to a llama.cpp you already run; *reaching* it (LAN, proxy, VPN)
  is the only connection concern.
- Not a Claude/frontier replacement. The goal is to make the **local** tier good enough that you reach for it first
  and **save frontier tokens** for the work that genuinely needs them.
- Not a general "any backend" client. It **targets llama.cpp** and uses its native capabilities as the differentiator.
  It stays OpenAI-compatible, so it *may* talk to other servers — but that is not the scope.

## Tech (planned)

- **Go** core + CLI + optional gateway (single static binary, dockerable / `.service`-friendly).
- **One shared web UI** across shells; desktop via **Wails** (all-Go) — or Tauri if a true native mobile app is later wanted.
- **No sync engine in the client** — v1 is single-machine with plain files you can point your own sync tool at;
  multi-device comes with the optional v2 gateway.

See [`docs/architecture.md`](docs/architecture.md) and [`docs/roadmap.md`](docs/roadmap.md).

## The name

**llamingo** is an Ecuadorian word (from Quechua *llamingu*) for the llama — and, in a second sense, for a crafted
*figure* of one. Fitting for a client that stands in front of, and represents, a headless engine. Styled **llaminGO**
because the core is written in Go.

## AI assistance

llaminGO is developed openly with the help of Claude (Anthropic). We state this plainly: commits Claude helped write
carry a `Co-Authored-By: Claude` trailer. The code and design are open source so the work can be inspected, reused, and
given back. If you'd rather not use AI-assisted software, that's a fair call — this disclosure is here so you can make it.

## License

**MPL-2.0** (to be added at publication).

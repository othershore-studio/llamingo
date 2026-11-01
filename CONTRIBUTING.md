# Contributing to llaminGO

Thanks for your interest. llaminGO is open source and contributions are welcome —
but it is a **solo-maintained, opinionated** project, so a few expectations up
front keep everyone's time well spent.

## How this works

- **Open an issue before non-trivial work.** For anything beyond a small fix,
  describe what you want to change and why, and wait for a thumbs-up before
  writing it. A big PR that doesn't fit is a loss for both of us.
- **Everything is reviewed.** All changes land through a pull request and are
  reviewed before merging. Direct pushes to `main` are not accepted.
- **No timeline.** This is a side project; reviews and replies may be slow, and
  that's expected — please don't read quiet as a no (or a yes).
- **Fit matters, and some things will be declined.** Changes that cut against
  llaminGO's direction (below) may be turned down even if they work. That's
  normal and not a judgement of the work.

## What llaminGO is (and isn't)

- A **client**, not a runtime: it drives a `llama-server` you already run and
  leans on its native API. It is not a model downloader or trainer, and not a
  general "any backend" client — it targets llama.cpp.
- **Local-first and lean.** Standard library first, few dependencies, features
  opt-in and transparent. The toolset stays small on purpose — a local model
  drowns in too many tools.
- **AI assistance is disclosed openly** — see the README. Contributions made with
  AI help are welcome; say so where it's relevant.

The architecture and the reasoning behind it live in [`docs/`](docs/)
(`architecture.md`, `ui.md`, `roadmap.md`); [`CLAUDE.md`](CLAUDE.md) has the
practical build-and-run notes.

## The bar for a PR

llaminGO ships with a GPU-free test suite and keeps it green — please hold that line:

- `go vet ./...` and `go test ./...` pass, and **new behavior comes with a test**.
  Tests run against the in-repo fake `llama-server` (`internal/lcpptest`) — no
  model, no GPU, no network.
- Route every model call through the one context builder (`internal/contextbudget`),
  so the honest token budget never drifts.
- Match the surrounding style; every `.go` file carries the **MPL-2.0 header**
  (Exhibit A — copy one from a neighbor).
- Keep commits focused, and the message explaining the *why*.

## Licensing

By contributing, you agree your contribution is licensed under the project's
**Mozilla Public License 2.0** (MPL-2.0), the same as the rest of llaminGO. No CLA.

## Security

Please **do not** open a public issue for a security problem. See
[SECURITY.md](SECURITY.md) for private reporting.

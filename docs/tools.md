# llaminGO — Native tools (Go-powered)

How llaminGO grows its **native tool** catalog cheaply: lean handlers over Go's standard
library and a small set of **pure-Go, permissively-licensed** libraries, each declaring
its risk tier into the permission gate that already exists. The goal is to **reduce
hand-coding** — reuse what Go (and the OSS ecosystem) already offers — without bloating
the default toolset or the dependency surface.

> A native tool is a thin `Registry` handler plus its MCP-hint risk tier; the gate and
> the risk defaults do the safety. So the marginal cost of a tool is "wrap a function."

## Library-selection policy

For a public **MPL-2.0** project that must cross-compile **CGO-free** to Windows, Linux,
macOS and **Android/Termux**, a dependency has to clear three bars:

1. **Pure Go, no cgo.** cgo binds a C library — it wants a C toolchain per target and
   breaks the CGO-free cross-compile that makes the static binary, gomobile, and Termux
   work at all. Pure Go is non-negotiable for a tool library.
2. **Permissive license.** MIT / BSD / Apache-2.0 (and MPL) compose cleanly with MPL and
   keep the project copyable. **Avoid (A)GPL** — strong copyleft pulls the whole project
   under it and breaks both the license and the ethos. (The ecosystem trend is toward
   permissive; lean into it.)
3. **Vendorable.** If a small library earns its place, **copy the source in** (license
   permitting) — it insulates against upstream vanishing and keeps the build hermetic.

**Stdlib first.** Go's standard library (its own BSD license — MPL-clean, zero deps)
covers a surprising amount; reach for a library only when it clearly earns the dependency.

## The stdlib "free tier"

Pure stdlib, zero deps, zero license question:

- **fetch / download** — `net/http` (honors `HTTPS_PROXY`), `net/url`.
- **read common formats** — `encoding/json`, `encoding/csv`, `encoding/xml`.
- **archives** — `archive/zip`, `archive/tar`, `compress/gzip`.
- **save / export / template** — `os`, `io`, `text/template`.
- **time, hashing, encoding** — `time`, `crypto/*`, `encoding/base64` · `hex`.

## The library tier (pure-Go, permissive)

Only when stdlib won't do:

| Need | Library | License |
|---|---|---|
| Markdown | goldmark | MIT |
| HTML parse | `golang.org/x/net/html` | BSD |
| YAML | `gopkg.in/yaml.v3` | MIT / Apache-2.0 |
| TOML | BurntSushi/toml | MIT |
| PDF text | pdfcpu | Apache-2.0 |
| XLSX | excelize | BSD-3 |
| Images (webp/tiff/resize) | `golang.org/x/image` | BSD |

*(Trap: some "office" libraries — e.g. unioffice — are AGPL/commercial. Skip them.)*

## Risk tiers & categories

Each tool declares its MCP hints, which drive the gate's defaults:

- **read-only, local** (format parse, file read) — cheap to grant (`allow this chat`).
- **openWorld** (fetch) — reaches the net; gated, and the consent prompt shows the target.
- **writes files** (save / export) — gated like the existing file tools.

That **read / openWorld / write** split is also the natural **category** axis (web ·
files · formats · data …). Ship a **small default set** and let a chat/project opt into
categories — the answer to small-model-tool-overload: a rich catalog, a lean default.

## Worked example — `fetch` (the policy is the code you don't skip)

`http.Get(url)` is three lines; a *safe* fetch tool is the wrapper around it:

- **SSRF guards** — scheme allowlist (`http`/`https` only, no `file://`/`gopher://`/
  `data:`); and **validate the resolved IP at dial time, per redirect hop** (a custom
  `Transport.DialContext`), rejecting private/loopback/link-local/metadata ranges (127/8,
  10/8, 172.16/12, 192.168, 169.254, `::1`, `fc00::/7` …). Validating at dial time — not
  the URL's hostname — is what defeats redirect-into-internal *and* DNS rebinding.
- **limits** — `Client{Timeout}` + an `io.LimitReader` size cap.
- **context hygiene** — text returns inline (capped); anything binary or large saves to
  the chat's `attachments/` and returns only its path — never base64 into context (the
  same out-of-band rule as image gen).
- **the gate** — `openWorld` + read-only (add the *write* tier if it saves); the prompt
  shows the exact final URL, so the user vetting the destination is itself an
  anti-injection check.
- **honest result** — echo the final URL (post-redirect), status, content-type, size;
  fail loudly on non-2xx / timeout / blocked-IP, never a silent empty.

Params: `url` (required), optional `save` / `to` (→ attachments, flips the write tier),
optional `max_bytes`.

The takeaway generalizes: the **transport is free, the policy is the code you keep.** A
local format-read tool needs almost none of this; an openWorld or writes-files tool needs
the wrapper. Curation keeps the active set lean regardless.

## Status

Exploration (2026-11-02). The gate, risk tiers, and `attachments/` already exist (image
gen, the file tools), so new tools slot onto proven machinery. Candidate first tools:
`fetch`, `time`, a format-read (json/csv). "Categorize the system tools" is the companion
surface that keeps the default lean.

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

## Rendering — the JS question (static vs headless)

`fetch` above is a **static** fetch: it reads the HTML the server sends. That is most
of what you'd want to read — news, docs, wikis, blogs, anything SSR or SEO-conscious —
*provided* you extract the article and never dump raw HTML into context (a readability
pass: `go-shiori/go-readability`, MIT, pure Go; or hand-rolled over `x/net/html` →
goldmark). A surprising amount of "JS site" content also rides in the initial HTML as
hydration JSON (`__NEXT_DATA__`, `__NUXT__`, `application/ld+json`) or sits behind a
clean JSON API the page itself calls — both reachable without executing a line of JS.

What static fetch **cannot** do is a client-rendered SPA: an empty shell painted by JS
after load. Rendering one needs a real browser engine, and **there is no pure-Go way to
do it** — goja runs JS but has no DOM/layout/fetch environment. `chromedp` / `go-rod`
are pure-Go *libraries* (they would pass the policy above) yet they drive an external
~150 MB browser binary — which fails the filter the policy *implies but does not state*:
the **runtime** footprint. It breaks zero-setup, will not run on Termux/mobile, and
torches the lean, copyable story.

So rendering is **not a native tool.** It is the native-vs-MCP line — the mirror of the
image-gen call (native *because* its setup was just a URL; a browser is a heavy install,
so it goes the other way → the MCP below). Native `fetch` stays static; rendered fetch is
a category you opt into. Anti-bot walls (Cloudflare challenges) beat both — fail honestly,
do not chase them.

### User-Agent

Set one. Go's default is `Go-http-client/1.1`, which many sites block or serve degraded
HTML to. Default to an **honest** UA that names llaminGO (`llaminGO/<ver> (+<repo>)`) and
make it **user-configurable** — a user can have a legitimate reason to present a browser
UA to a site that demands it; that is their transparent call, not a default we make for
them. Send `Accept` / `Accept-Language`; let `net/http` handle gzip. A single
user-initiated fetch is not a crawler, so no robots stack is needed — identify, do not
hammer. (Fetched text is **untrusted input**: the gate showing the final URL is the
anti-injection check, and the model treats page content as data, never instructions.)

## Headless rendering — a separate MCP (the next big feature)

The rendering tier lands as a **standalone MCP server**, not in the core. Keeping it out:

- **Frees the stack.** An MCP server is any language, so rendering escapes the pure-Go /
  no-cgo constraint entirely — it can drive a real browser via Playwright or WebDriver
  without touching the lean Go binary.
- **Isolates the weight.** The ~150 MB browser, its updates, and its failure modes live
  in one opt-in process, added per chat/project like any MCP — the default set stays lean.
- **Stays independently useful.** A well-formed fetch/render MCP (typed tool hints, bulk
  bytes out-of-band, an ok/refused/error result) is reusable by any MCP client, which also
  makes it easy to test outside llaminGO.

**Engine: Gecko (Firefox), not Chromium.** Firefox is **MPL-2.0** — llaminGO's own
license — so it composes cleanly and stays on-ethos, where Chromium's BSD would merely be
*allowed* (Gecko is the on-license, on-values pick, and a deliberate preference). The
render MCP returns the **same shape** as native `fetch` (clean markdown/text; bulk to
`attachments/`), so a chat swaps static for rendered fetch without the model relearning
the tool.

*Planned, not built (2026-11-08): native static `fetch` + readability first, the Gecko
render MCP after.*

## Status

Exploration (2026-11-02). The gate, risk tiers, and `attachments/` already exist (image
gen, the file tools), so new tools slot onto proven machinery. Candidate first tools:
`fetch`, `time`, a format-read (json/csv). "Categorize the system tools" is the companion
surface that keeps the default lean. The **Gecko render MCP** (above) is the named next
big feature — native static `fetch` + readability lands first.

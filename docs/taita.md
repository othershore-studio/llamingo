# Taita — the llaminGO server (v2)

**Taita** is llaminGO's optional multi-user gateway: one extra binary that serves the web UI to a plain
browser and gives every session its own store, so a household (or a small team) on a private mesh can share
one `llama-server` through one URL. The name is Kichwa/Andean Spanish for *father / elder / keeper* — the
provider that sits in front of the engine.

Taita is **grown from `cmd/llamingo-serve`** and shares the same core (`internal/httpapi` + `internal/store`);
it is **not** a fork and **not** a flag on the client. Same repo, shared core, so there is no sync problem
between the client and the server.

> Status: in active build on branch `taita-foundation` (local, unpushed). P0 done, P1 essentially working,
> P3 mechanism landed; **P2 (accounts) is the current phase.** See [Phase plan](#10-phase-plan--status).

---

## 1. What it is, in one paragraph

`llama-server` is single-tenant and has no UI. The llaminGO *client* (desktop/CLI/mobile) is one person's
app. Taita is the piece in between: a long-running server on the mesh that (a) serves the chat UI to any
browser, (b) keeps each visitor's chats/memory/images in their own store, and (c) funnels everyone through
the one backend and the one Forge image queue. The frictionless default is **no login** — presence is
identity (profiles, not passwords); accounts are opt-in and mainly exist to make a profile portable and to
mark the one admin. See [`user-not-admin-lens`] thinking throughout: design for the *user* who just wants to
chat, not the admin.

## 2. Invariants & non-goals

- **`internal/httpapi` is not modified for multi-tenancy.** Per-user isolation is achieved by composing many
  single-store cores, never by making one core multi-store (see §4). The desktop/mobile/CLI callers of
  `httpapi.New(be, st)` must keep working unchanged — *never a push that adds the server but breaks a client.*
- **Data stays in files.** Chats, memory, images live in the two-root store exactly as today. The only
  database is the **accounts registry** (bbolt), and it holds accounts *only* (see §6).
- **Clients stay thin; there is no sync engine.** Remote chats are a **cache** on the client, with Taita as
  the source of truth (§5). "Download a chat," not "sync a chat."
- **One process, one backend, one Forge queue** (§7). Per-user/per-chat connection *switching* is deferred to
  P4 (named connections).
- **CGO-free, few deps, permissive licenses** (repo policy). bbolt (MIT, pure Go) and `golang.org/x/crypto`
  (already a dep) are the only additions for accounts.

## 3. The browser-surface invariant (the CORS story)

This is the rule that has drifted across compactions more than once. **Which surfaces may a browser open?**

| Surface | Browser-facing? | Why |
|---|---|---|
| **Desktop (Wails)** | Yes — the only browser-facing *local* client | Its webview is Origin-gated by `httpapi.CORS` (the Wails webview origins), so no stray web page can reach the loopback core, which holds the backend token and the store. See [`loopback-api-origin-gate`]. |
| **`llamingo-serve`** | **No — API only** | It is the headless loopback core. Giving it a web UI bypasses the CORS gate and makes the backend token reachable from any page. A `-web` flag was tried and **reverted** (commit `cca6898`). |
| **`taita`** | Yes — the *server* surface | Purpose-built to serve a browser. It needs its **own Origin gate** before any public (tunnel) exposure; on a trusted LAN the same-origin SPA + HttpOnly/SameSite=Lax session cookie is enough. |

So the only two browser surfaces are the **desktop webview** and **taita**. `llamingo-serve` sits behind the
desktop as its core and is never pointed at by a browser.

**The P3 proxy sidesteps CORS by design** (§5): the browser talks only same-origin to its own sidecar, and
the sidecar→taita hop is server-side (a Go `http.Client`, no `Origin` header), so an Origin gate on taita
never blocks a legitimate client.

## 4. Foundation — the per-session Server registry

`httpapi.Server` holds **one** store (`s.st`). The naive path to multi-tenancy — resolve the store per
request inside `httpapi` — was **rejected after reading the code**: `Server` has `sync.Mutex` fields (can't be
value-copied per request), ~70 `s.st` call sites across 9 files, and shared `grants`/`gates` maps that would
leak across users.

Instead, a new package **`internal/taita`** composes **N single-store `httpapi.Server`s — one per session,
cached, keyed by session**:

```
taita.New(be, dataRoot, uiFS)        // one gateway
  coreFor(key) -> httpapi.New(be, store.Open(dataRoot/<key>))   // lazy, cached
```

`internal/httpapi` changes **zero lines** (it only gained a variadic `Option` earlier, which existing callers
ignore). Per-user isolation — separate gates, memory, MCP sessions — is then *free*, because each user simply
has their own core over their own store. Verified safe to instantiate per-user: `httpapi.New` starts no
goroutines and connects no MCP until a turn runs; `genSem`/`lastModel` are package-global so the Forge queue
stays global across all cores (§7).

Request routing in `taita.ServeHTTP`:
1. Ensure an anon-session cookie (`taita_sid`, HttpOnly, SameSite=Lax) → hash → session key `annon/<hash>`.
   Once accounts land (§6), an authenticated session keys to `users/<name>` instead.
2. `/api/*` → `coreFor(key).ServeHTTP` (the per-session core).
3. anything else → the embedded SPA.

## 5. Store model — two roots = cache, not sync

The store already has two roots: `chat/` (free chats) and `folder/` (projects). That split *is* the
client/server boundary:

- **`chat/` is remote** when a client is linked to Taita — cached on the client, Taita is source of truth, no
  sync engine.
- **`folder/` (projects) stays local** — projects need a local llaminGO (local files, project MCP, etc.).

**Two chat lists.** A real client can hold chats of two origins: **local-only** (made direct-to-llama) and
**server** (cached from a linked taita). So the client UI shows **two lists** — "Local Chats" + "Server
Chats" — which also makes cache-vs-local visible. A browser served *by* taita has no local store, so it shows
**one** list (every chat is a server chat). `App.svelte` is shared, so it adapts: **1 list when taita-served
(`serverMode`), 2 lists on a local client with a link (`serverLinked`).**

- `GET /api/mode` → `{mode: "local"|"server", serverLinked: bool}`. `ServerMode()` is an `httpapi.Option` set
  by `internal/taita`; it is backend-independent (unlike `/props`).
- P3 proxy: `httpapi.WithTaita(url)` / `setTaita(url)` gives the sidecar a cookie-jar `http.Client`;
  `GET/POST /api/server/chats` proxies to `<taita>/api/chats`. The cookie jar keeps the sidecar pinned to one
  server-side folder. `config.TaitaURL` persists the link and is editable live in the Settings window
  (`PUT /api/settings` → `setTaita`).

**Self-loop gotcha:** if the Taita URL points back at the same server/store (e.g. the client's own loopback),
the proxy fetches that server's own `/api/chats` and "Server Chats" shows the local chats duplicated. Point
the link at a *separate* taita (its own store).

**Long chats** compose three levers: the list is metadata-only (title/updated/size) so it's instant with
thousands of chats; tail-load within a chat (last screenful, page older on scroll); and compaction already
shadows old ranges with summaries.

## 6. Identity & claim — accounts in bbolt, everything else in files

The account registry is **table-shaped** (username → hash + profile + quota), so it lives in **one store, not
scattered `account.json` files**: adding a field is one struct change, not N per-file migrations. The store is
**bbolt** — a pure-Go KV DB (`data/accounts.db`), *a database, not SQL*; never cgo/`mattn`, never
modernc/sqlite. It sits behind a thin **`Accounts` interface** (`Get`/`Put`/`List`/`Delete`), the same seam
discipline as `Backend`/`lcpptest`: tests use an in-memory fake, and the "repo vanishes" dependency risk
collapses to a ~50-line JSON impl behind the same interface.

The DB holds **accounts only.** Chats, memory and images stay files/folders — no DB-creep, the two-root cache
model untouched.

**Anon stays no-DB.** cookie → hash → `data/annon/<hash>/`; folder-exists = the session. A per-folder sidecar
`session.json` carries `created`/`lastSeen`/`claimed` — **never filesystem mtime** (migrations/backups/`cp`
mangle dates; timestamp-as-data survives, like the store's `meta.json`). The principle that dissolves the
files-vs-DB debate for anon: **folders are authoritative; any index is a derived, rebuildable cache, never a
second source of truth.** So:

- **Anon volume** is a hygiene *drip*, not a capacity risk — incognito / cleared-cookie / new-device sessions
  mint fresh folders that are never claimed. Fix at the source: a short `lastSeen`-based **TTL on unclaimed
  anon** (incognito orphans never return, so they age out; a returning user bumps `lastSeen`).
- Keep files neat with **date-bucketed dirs** (`annon/YYYY-MM/<hash>/` → coarse purge is one `rm -rf` of a
  cohort) + the sidecar for fine/claimed state.
- **Admin dashboard = walk + cache** (each folder = a row); purge = delete folders, works with `ls`/`rm` with
  the app off. Add a bbolt *anon index* only if the pile outgrows a per-sweep walk — and then as a cache
  rebuilt from folders, not authoritative. Accounts stay the real bbolt table.

**Claim = recompute the cookie hash + merge that anon folder on login** (per-cookie, so cross-device claim is
free: logging in on device B absorbs B's anon chats). IDs are slug+hash so merges don't collide; delete the
anon folder once claimed.

**Roles.** `admin` vs `user`. The Taita server's own config (connections / Forge / MCP — the centralized
config in `config/`) is edited **only** through an admin-only settings UI. Regular users and anon get **no
settings, just chat** — config is the admin/owner's job. The admin is named by `-admin <name>` / `$TAITA_ADMIN`
(that account becomes admin on sign-in, no land-grab), or defaults to the first account to sign in; either way
the admin must set a password. `taita admin` (console, server stopped — the bbolt DB is single-writer) recovers
it: `list` / `set NAME [admin|user]` / `passwd NAME`.

**Accounts are a P2 artifact** — P0/P1 are anon-only, pure files.

## 7. The Forge global queue (inherited) + the trust gradient

`internal/imagegen` already serializes generation with a **package-level semaphore `genSem` (cap 1)** —
"serializes image generation to one at a time across the whole app" (proven by `TestGenerationIsSerialized`).
Because taita is **one process** and the image tool runs server-side in `mcp.Run` (every instance sharing
`genSem`), the global queue is respected across all users/chats **automatically**. **The refactor's one rule:
keep `genSem` process-global** — never move it into per-user/per-request scope.

Multi-user adds work v1 never needed (deferred): queue **visibility** (emit a "queued #N" SSE event instead of
blocking silently), **fairness** (per-user in-flight caps so one user can't hog the GPU), and the deeper
**image-vs-chat GPU collision** (`genSem` guards image-vs-image only; an image gen can still OOM a concurrent
llama turn — see [`shared-gpu-contention-kills-llama`]). A Forge↔llama interlock is where quotas eventually
live.

**Trust gradient — what taita exposes to whom:**

| Capability | Who | Phase |
|---|---|---|
| Chat + memory | everyone (anon included) | P1 |
| Forge image gen | logged-in accounts (protects the shared GPU from anon abuse; gives login a concrete reason) | P2 |
| MCP | admin-curated, logged-in (stdio spawn / outbound = highest risk; collides with [`small-model-tool-overload`]) | P4 |

llama is proxied by definition (client→taita→llama is just how `httpapi` already works); "serving" these isn't
new plumbing, it's this access gradient.

## 8. Dirs

- **`config/`** — server settings: the connections registry, Forge, MCP, listen addr, auth secret.
- **`data/`** — per-user/anon stores + images + `accounts.db`.

Both settable by env/param (Docker volumes). Defaults: `~/.taita/config` + `~/.taita/data` (hidden dirs,
matching `~/.llamingo`).

**Config/data split is forced, not optional:** config is rooted *in the store* today (`imagegen.json`, global
`mcp.json`, the connection list all under the store root). A per-user store would wrongly make Forge/MCP/llama
per-user, so those ~4 handlers (settings/connections/imagegen/mcp) repoint off `s.st` to the server `config/`
root. That repoint *is* config centralization: one URL, all clients inherit.

## 9. Web UI delivery

**Chat-only** (projects need a local llaminGO), **`go:embed`'d** into the `taita` binary. Because `go:embed`
can't reach `../../web/dist`, **`cmd/taita/build.sh` stages** `web/dist` → `cmd/taita/web/dist` before
`go build` (`cp -r` preserves `assets/`), exactly like `mobile/build-apk.sh`. Rejected alternatives: a single
shared-root embed (would force the Wails desktop `main.go` off the repo root — too risky to the desktop
client) and disk-serve (gives up the single-file binary). `build.ps1` was dropped — Git Bash ships with Git
for Windows, so one `.sh` is enough.

## 10. Phase plan & status

- **P0 — foundation** ✅ `internal/taita` per-session registry + anon cookie + `/api`-vs-SPA mux + the
  `cmd/taita` binary with the embedded UI; config/data dirs. Web validated in a browser vs `-fake` (full chat
  round-trip streamed).
- **P1 — free gateway (MVP)** — anon sessions + embedded chat UI + no login. The frictionless family chat on
  the mesh. (Essentially working from P0; needs the config/data split finished to be clean.)
- **P2 — accounts** ◀ *current* — the bbolt `Accounts` registry + optional login (bcrypt) + claim/merge +
  **roles** (admin vs user) + an admin-gated server-config UI. Forge becomes a logged-in perk.
  **Done:** the accounts seam (`b7a41ba`); login/logout/whoami + folder-anchored session identity (`ec7b460`);
  the login UI — sidebar sign-in/profile + admin-gated settings, browser-verified vs `-fake` (`7e279da`);
  **claim/merge** — a session's anonymous chats follow it into the account on login, moved not copied
  (`Store.AbsorbChats`, `78e4d55`); **admin designation + recovery** — `-admin`/`$TAITA_ADMIN` names the admin
  (else first-to-sign-in), plus a `taita admin` console CLI (`list`/`set`/`passwd`) for recovery (`f65b7e6`).
  **Remaining:** the config/data split + **server-side** role enforcement —
  today roles are UI-gated only (a non-admin's browser hides Settings, but the per-user core would still
  answer `/api/settings` for that user's own store). Likely shape: an additive `httpapi.WithConfigStore`
  Option (default = the data store, so desktop/mobile/CLI stay byte-for-byte) + taita opens one shared config
  store and gates config *writes* to the admin.
- **P3 — clients see remote** — mechanism landed: the two-list split + the sidecar→taita proxy + the Settings
  URL field (commits `0a015fc`, `7d1b47f`, `106a94c`; round-trip unit-tested). A *meaningful* client
  connection (persistent, cross-device identity) needs P2 accounts — anon only tests the plumbing.
  **Next sub-steps:** proxy *open + stream* a server chat (`/api/server/conversations/{id}` +
  `/api/server/chat/stream`) + a "+ New" on the Server list; then the download-by-chat cache.
- **P4 — long tail** — lazy chat loading, quotas, centralized connections/Forge/MCP config, per-chat named
  connections, the Forge↔llama GPU interlock, taita Origin gate + hiding the backend token for public exposure.

Client-connection path: **P1 anon browser ✅ → P2 accounts → P3 desktop/client repoint with a real user.**

## 11. Open decisions

1. **config/ vs data/ split** — settings in `config/`, per-user in `data/`. *Locked: yes* (it's forced, §8).
2. **Account model** — **LOCKED (2026-11-03): hybrid.** Passwordless profiles by default (presence = identity
   on the trusted mesh); a profile *may* set a password (opt-in); the **admin account requires one** (it gates
   server config). Matches both the user-first lens and public-safety. Login = "become this profile": an
   existing passwordless name you just become, a password-protected name requires the password, an unknown
   name is created (passwordless) and becomes yours — the Netflix-profile UX.
3. **Admin designation** — **LOCKED (2026-11-03): option A.** `-admin <name>` / `$TAITA_ADMIN` names the admin
   (becomes admin on sign-in, no matter who logs in first); with no designation, the first account to sign in
   is admin (zero-config fallback). Either way the admin must set a password. Recoverable via the `taita admin`
   console CLI (`list` / `set` / `passwd`), run on the server box with the server stopped.
4. **P1 auth posture** — presence / shared mesh token; real auth deferred to public (cloudflared) exposure.

---

*Design rationale originated in session memory (`taita-build-plan`, `v2-taita-direction`); this doc is the
checked-in home. Keep it current as phases land — it is re-read after every compaction, unlike memory.*

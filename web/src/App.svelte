<script>
  import { onMount } from 'svelte'
  import MarkdownIt from 'markdown-it'

  // Render assistant markdown safely: html:false escapes any raw HTML the model
  // emits, so {@html} only ever sees markdown-generated tags.
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true })

  // The desktop app (Wails) cannot stream through its asset server, so it serves
  // the core API on a loopback port the webview calls directly; the browser and
  // PWA use same-origin relative paths.
  const apiOrigin = () => (window.runtime ? 'http://127.0.0.1:8766' : '')
  const apiFetch = (path, opts) => fetch(apiOrigin() + path, opts)

  // On touch devices (phone) Enter inserts a newline and the Send button sends
  // (WhatsApp-style); a mouse/desktop keeps Enter-to-send.
  const coarsePointer = typeof window !== 'undefined' && !!window.matchMedia && window.matchMedia('(pointer: coarse)').matches

  // Playful "working" words shown while a turn runs — llama / Andean / Ecuador
  // themed. Edit this list freely (add your Ecuadorian picks); one is chosen per
  // turn and rotates every few seconds. SPINNER is the braille animation frames.
  const WORKING_WORDS = ['Rumiando', 'Pensando', 'Trotando', 'Pastando', 'Tejiendo', 'Grazing', 'Trekking', 'Humming', 'Pronking', 'Soaring']
  const SPINNER = '⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
  const pickWord = () => WORKING_WORDS[Math.floor(Math.random() * WORKING_WORDS.length)]

  // Rewrite local attachment refs (attachments/<name>, as written in the log) to
  // the serve route for the open conversation, so persisted and generated images
  // load in the desktop app and the browser alike. Remote URLs pass through.
  const renderContent = (text) =>
    md.render(text).replaceAll('src="attachments/', `src="${apiOrigin()}/api/conversations/${activeId}/attachments/`)

  // Parked, in step with the server (stripLeadingToolBlob): the live mirror of
  // the ReAct-blob strip. Both are currently off so we can measure how often the
  // model emits a {"action":…} scratch blob and how stable that is. Re-enable by
  // wrapping the renderContent argument in stripToolBlob(...) again. It holds a
  // still-streaming blob back until it closes so it never flashes.
  function stripToolBlob(text) {
    const t = text.replace(/^\s+/, '')
    if (!t.startsWith('{')) return text
    const end = jsonObjectEnd(t)
    const head = end < 0 ? t : t.slice(0, end + 1)
    if (!head.includes('"action"') && !head.includes("'action'")) return text
    if (end < 0) return '' // the blob is still streaming — hold it back
    let rest = t.slice(end + 1).replace(/^\s+/, '')
    // If the model buried the image inside the blob with nothing after it,
    // recover it so the reply is not empty.
    if (!rest.includes('](')) {
      const img = (head.replace(/\\"/g, '"').replace(/\\n/g, '\n').match(/!\[[^\]]*\]\([^)\s]+\)/) || [])[0]
      if (img) rest = rest ? img + '\n\n' + rest : img
    }
    return rest
  }
  function jsonObjectEnd(s) {
    let depth = 0,
      inStr = false,
      esc = false
    for (let i = 0; i < s.length; i++) {
      const c = s[i]
      if (esc) esc = false
      else if (inStr && c === '\\') esc = true
      else if (c === '"') inStr = !inStr
      else if (inStr) continue
      else if (c === '{') depth++
      else if (c === '}' && --depth === 0) return i
    }
    return -1
  }

  // In the Wails desktop shell the window is frameless, so the HTML topbar is the
  // title bar: draggable, with our own min/max/close. The browser/PWA has none.
  const isDesktop = typeof window !== 'undefined' && !!window.runtime
  // The mobile app is "desktop minus Projects" (a desktop/coding concern). The
  // Android shell loads the UI with `?app=android` — an explicit, reliable marker,
  // rather than inferring "mobile" from `(pointer: coarse)` (unreliable in a WebView,
  // and it would wrongly hide Projects in any touch browser). coarsePointer stays for
  // its real job: Enter-to-send gating on a virtual keyboard.
  const mobileClient =
    typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('app') === 'android'

  let conn = $state(null) // { model, nCtx }
  let chats = $state([])
  // Server/Taita chats on a LOCAL client are a cache of a linked taita (wired in
  // P3); serverLinked flips true once a taita connection is configured. In a
  // taita-served browser serverMode is true and `chats` already IS the server's.
  let serverChats = $state([])
  let serverLinked = $state(false)
  let serverMode = $state(false) // this core is a taita server (GET /api/mode)
  // Accounts (server/taita mode only): whoami drives who is signed in and whether
  // the admin-only server settings are offered. null until resolved; {user:null}
  // while anonymous.
  let me = $state(null)
  let loginOpen = $state(false)
  let loginName = $state('')
  let loginPassword = $state('')
  let loginError = $state('')
  let loginBusy = $state(false)
  let activeId = $state(null)
  let streamingChatId = $state(null) // the chat the in-flight stream belongs to
  let streamMessages = null // its live message array, so switching chats mid-stream keeps the turn alive
  let messages = $state([]) // { role, content }
  let input = $state('')
  let inputEl = $state(null) // the composer textarea, for auto-grow
  // Grow the composer with its content (capped, then it scrolls), and spring back
  // to one line after send() clears it — runs on every keystroke and on clear.
  $effect(() => {
    input // track the value
    const el = inputEl
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 160) + 'px'
  })
  let streaming = $state(false)
  // "Working" indicator: a braille spinner + a themed word while a turn runs.
  let spinnerFrame = $state(0)
  let workingWord = $state(WORKING_WORDS[0])
  let toolRunning = $state(false) // a tool is executing — keep the indicator up even when narration is shown
  $effect(() => {
    if (!streaming) {
      spinnerFrame = 0
      return
    }
    workingWord = pickWord()
    let i = 0, t = 0
    const id = setInterval(() => {
      spinnerFrame = i = (i + 1) % SPINNER.length
      if (++t % 375 === 0) workingWord = pickWord() // rotate the word ~every 30s
    }, 80)
    return () => clearInterval(id)
  })
  // The indicator's label: during an image generation, name the model so a slow
  // model-load reads as progress, not a freeze ("Generating with Juggernaut…").
  // Otherwise the playful themed word. The model comes from the tool-call args
  // when the model named one; a blank falls back to a generic phrase.
  function workingLabel(m) {
    if (toolRunning && m?.tools?.length) {
      const t = m.tools[m.tools.length - 1]
      if (t?.name === 'generate_image') {
        let model = ''
        try {
          model = JSON.parse(t.arguments || '{}').model || ''
        } catch {}
        return model ? `Generating with ${model}` : 'Generating an image'
      }
    }
    return workingWord
  }
  // Auto-scroll: follow the latest message as it streams, unless the reader has
  // scrolled up (then the "↓ Latest" pill invites them back).
  let threadEl = $state(null)
  let atBottom = $state(true)
  function onThreadScroll() {
    const el = threadEl
    if (el) atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80
  }
  function scrollToLatest() {
    const el = threadEl
    if (el) { el.scrollTop = el.scrollHeight; atBottom = true }
  }
  $effect(() => {
    // Re-run when the thread grows: a new message, or more streamed content /
    // reasoning on the last one. Only follow when the reader is already at the end.
    const last = messages[messages.length - 1]
    void (messages.length + (last?.content?.length ?? 0) + (last?.reasoning?.length ?? 0))
    if (atBottom && threadEl) threadEl.scrollTop = threadEl.scrollHeight
  })
  let budget = $state(null) // { used, nCtx, level }
  let error = $state(null)
  let convSummaries = $state([]) // compaction summaries, each anchored at its fold point in the thread
  let report = $state(null) // transient compaction report
  let compacting = $state(false) // a manual compaction is in flight (shows an in-thread cue)
  let editingId = $state(null) // conversation id being renamed inline, if any
  let editTitle = $state('') // the in-progress rename text
  let confirmDeleteId = $state(null) // conversation id awaiting delete confirmation
  let copiedAt = $state(null) // index of the message whose copy button just fired

  let projects = $state([])
  let expandedProject = $state(null)
  let sessions = $state([]) // sessions of the expanded project
  let openingFolder = $state(false)
  let folderPath = $state('')

  let memoryOpen = $state(false)
  let memoryContent = $state('')
  let memoryTampered = $state(false)

  let settingsOpen = $state(false) // connection settings modal open?
  let connections = $state([]) // saved llama-server connections: [{name, url, token, _id}]
  let activeUrl = $state('') // the connection the backend is pointed at now
  let connOpen = $state(false) // status-bar connection picker open?
  let connId = 0
  let activeConnId = $state('') // id of the active connection (NOT the active chat — that's activeId)
  const numOrNull = (v) => (v === '' || v == null ? null : Number(v))
  const newConnId = () => (typeof crypto !== 'undefined' && crypto.randomUUID ? crypto.randomUUID() : 'c' + Date.now().toString(36) + Math.random().toString(36).slice(2))
  // A connection is "active" by its stable id, so two personas on the same url are
  // told apart — and the match survives editing a connection's url or name.
  const isActiveConn = (c) => !!activeConnId && c.id === activeConnId
  let settingsLocation = $state('') // editable user location (for place-aware tools)
  let settingsForgeUrl = $state('') // editable Forge Neo image-server URL
  let settingsTaitaUrl = $state('') // editable Taita server URL (links this client's Server Chats)
  let settingsDebug = $state(false) // debug logging: raw llama/Forge/tool payloads to each chat's logs/
  let autostartSupported = $state(false) // this build can manage login autostart (desktop, per-OS)
  let autostartEnabled = $state(false) // launch llaminGO at login
  let autostartMinimized = $state(false) // the login launch starts hidden in the tray
  let imageModels = $state([]) // image models: [{name, autoPick, default, promptStyle, note}]
  let imageDefault = $state('') // chosen default model name
  let imageFetching = $state(false) // a fetch-from-Forge is in flight
  let imageError = $state('') // last fetch error, shown inline
  let toolsOpen = $state(false) // per-chat tools modal open?
  let toolGroups = $state([]) // per-chat tools: [{kind, name, tools:[{name, description, enabled}], _open}]
  let mcpServers = $state([]) // Settings MCP servers: [{name, transport, url, header, editable, connected, _id}]
  let mcpLoaded = $state(false) // did GET /api/mcp succeed? (guards save from wiping on a failed load)
  let mcpId = 0 // stable keys for MCP server rows
  let projectOpen = $state(false) // ⋮ Project surface (project/folder settings)
  let projectInProject = $state(false) // is the active chat a project session?
  let projectSystem = $state([]) // project system-tool defaults: [{name, description, enabled}]
  let projectMcp = $state([]) // project MCP servers (read-only)
  let projectShowWorkingDir = $state(true) // prepend the "your working directory is …" note
  let budgetOpen = $state(false) // the status-bar Context popover (bar + count + Compact)

  let reachable = $state(true)
  let staleNote = $state(null) // transient "server changed" note

  let pending = $state(null) // a tool's consent prompt: { id, label, detail, risk, scopes }

  let thinking = $state(true) // per-turn thinking toggle; only shown when the model supports it
  let sysMenu = $state(false) // desktop window menu open?
  let actionsMenu = $state(false) // topbar ⋮ menu (Memory / Settings) open?
  let onTop = $state(false) // always-on-top state
  let maximized = $state(false) // desktop window maximized? drives the maximize/restore icon
  let sidebarOpen = $state(loadSidebar()) // left panel shown? persisted per browser
  let showDetail = $state(loadShowDetail()) // show the thinking trace + "used X" tool chips? (view-only, per browser)
  const SIDEBAR_MIN = 180, SIDEBAR_MAX = 460 // sidebar drag-resize clamp (px)
  let sidebarWidth = $state(loadSidebarWidth()) // left panel width, drag-resizable + persisted
  // Thin-window (drawer) mode. The breakpoint lives here in JS as the
  // single source of truth; the .narrow class drives the CSS. drawerOpen is the
  // thin-mode overlay's visibility — transient and never persisted, so hiding the
  // sidebar on a thin window can't clobber the user's saved sidebarOpen preference.
  const NARROW_BP = 700 // px; at or below this the sidebar is an overlay drawer
  let narrow = $state(false)
  let drawerOpen = $state(false)
  // What the sidebar actually shows: the saved preference when wide, the transient
  // drawer when thin. Growing the window back therefore restores the wide state.
  const sidebarShown = $derived(narrow ? drawerOpen : sidebarOpen)

  onMount(() => {
    refresh().catch((e) => (error = String(e)))
    loadConnections() // so the status bar can show the active connection's name
    const t = setInterval(checkServer, 15000)
    // On resize: keep the sidebar from crowding out the conversation, and track
    // whether the window is maximized (drives the maximize/restore icon).
    const onResize = () => {
      sidebarWidth = Math.max(SIDEBAR_MIN, Math.min(sidebarWidth, window.innerWidth - 360))
      syncMaximized()
      // Thin-window mode from the same resize signal (fires in a real window and
      // under emulation). Reset the drawer only on an actual wide/thin switch, so a
      // thin window opens with the conversation visible and sidebarOpen is left be.
      const n = window.innerWidth <= NARROW_BP
      if (n !== narrow) {
        narrow = n
        drawerOpen = false
      }
    }
    onResize()
    window.addEventListener('resize', onResize)
    return () => {
      clearInterval(t)
      window.removeEventListener('resize', onResize)
    }
  })

  // Reflect the window's maximized state so the title-bar button shows the right
  // glyph (maximize vs restore). Wails only; a no-op in the browser/PWA.
  async function syncMaximized() {
    try {
      maximized = (await window.runtime?.WindowIsMaximised?.()) ?? false
    } catch {
      maximized = false
    }
  }

  // Monitor the server: warn if the model or context size changed under us, and
  // reflect when it becomes unreachable.
  async function checkServer() {
    try {
      const p = await apiFetch('/api/props').then((r) => {
        if (!r.ok) throw new Error('unreachable')
        return r.json()
      })
      reachable = true
      if (!conn) {
        // Came up after starting with the server unreachable: adopt props now.
        conn = p
      } else if (p.model !== conn.model || p.nCtx !== conn.nCtx) {
        staleNote =
          p.model !== conn.model
            ? `Server model changed to ${modelName(p.model)}`
            : `Server context changed to ${p.nCtx}`
        conn = p
      }
    } catch {
      reachable = false
    }
  }

  async function refresh() {
    // A failing /props (e.g. the model is down) must not block the chat list,
    // which comes from the local store and is always available: load props on
    // its own and fall back to "unreachable" instead of throwing.
    try {
      const r = await apiFetch('/api/props')
      if (!r.ok) throw new Error('props unavailable')
      conn = await r.json()
      reachable = true
    } catch {
      conn = null
      reachable = false
    }
    // Mode is server-declared (backend-independent), so the layout is correct even
    // when the model is down: taita → "server" (one chat list, no Projects).
    try {
      const m = await apiFetch('/api/mode').then((r) => r.json())
      serverMode = m?.mode === 'server'
      serverLinked = !!m?.serverLinked
    } catch {}
    // In server mode, resolve the signed-in identity (anonymous → {user:null}).
    if (serverMode) {
      try { me = await apiFetch('/api/whoami').then((r) => r.json()) } catch { me = null }
    } else {
      me = null
    }
    chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
    serverChats = serverLinked ? await apiFetch('/api/server/chats').then((r) => (r.ok ? r.json() : [])).catch(() => []) : []
    projects = serverMode ? [] : ((await apiFetch('/api/projects').then((r) => r.json())) ?? [])
    if (!activeId) {
      if (chats.length) await selectChat(chats[0].id)
      // No chats yet: land in a ready one so the toolset + budget show from message
      // zero (they're gated on activeId). Only when reachable, so a down backend
      // leaves the calm empty state rather than erroring on create.
      else if (reachable) await newChat()
    }
  }

  // Open a conversation: the core owns history, so load its log from disk.
  async function selectChat(id) {
    if (narrow) drawerOpen = false // thin mode: reveal the conversation after picking from the drawer
    // Returning to a chat whose stream is still in flight: show its own live array
    // so the in-progress turn keeps rendering, instead of reloading a partial copy
    // from disk and losing the stream.
    if (id === streamingChatId && streamMessages) {
      activeId = id
      messages = streamMessages
      convSummaries = []
      return
    }
    activeId = id
    error = null
    budget = null
    report = null
    const entries = (await apiFetch(`/api/conversations/${id}`).then((r) => r.json())) ?? []
    // Pair each persisted reasoning entry with the assistant answer that follows it,
    // and anchor each compaction summary where it ran: after the message it followed
    // in the log (the summary entry is appended at that moment), shown with its time.
    const msgs = []
    const sums = []
    let trace = ''
    let tools = []
    for (const e of entries) {
      if (e.kind === 'reasoning') {
        trace = e.content
      } else if (e.kind === 'toolcall') {
        tools.push({ name: e.name, arguments: e.content }) // name + args persisted; args drive the hover tooltip
      } else if (e.kind === 'msg') {
        msgs.push({ role: e.role, content: e.content, reasoning: e.role === 'assistant' ? trace : '', tools: e.role === 'assistant' ? tools : [], time: e.time, tokPerSec: e.tokPerSec })
        trace = ''
        tools = []
      } else if (e.kind === 'summary') {
        sums.push({ content: e.content, time: e.time, afterIndex: msgs.length - 1, saved: Math.max(0, (e.replaced ?? 0) - (e.summaryTokens ?? 0)) })
      }
    }
    messages = msgs
    convSummaries = sums
    atBottom = true // opening a chat lands on its latest message
    loadBudget(id)
  }

  // The honest context budget for a chat's current state, fetched on open so the
  // status bar shows usage before the first message and again on reopen — not
  // only after a turn's `done`. Guarded against a fast switch; a failed fetch
  // leaves the bar as it was rather than flashing a stale chat's number.
  async function loadBudget(id, fresh = false) {
    try {
      const b = await apiFetch(`/api/conversations/${id}/budget${fresh ? '?fresh=1' : ''}`).then((r) => (r.ok ? r.json() : null))
      if (b && activeId === id) budget = b
    } catch {
      /* leave the bar as-is */
    }
  }

  // Compaction folds the oldest messages into a summary (non-destructive); the
  // core returns how many it folded and the tokens saved.
  async function compact() {
    if (!activeId || streaming || compacting) return
    compacting = true
    try {
      const r = await apiFetch('/api/compact', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ id: activeId }),
      }).then((r) => r.json())
      await selectChat(activeId)
      report =
        r.folded > 0
          ? `Folded ${r.folded} message${r.folded === 1 ? '' : 's'}, saved ~${r.saved} tokens`
          : 'Nothing old enough to compact yet'
    } finally {
      compacting = false
    }
  }

  // Personal memory is injected into every prompt (metered as its own segment)
  // and editable here. An edit made outside the app is flagged until accepted.
  async function openMemory() {
    const m = await apiFetch('/api/memory').then((r) => r.json())
    memoryContent = m.content ?? ''
    memoryTampered = !!m.tampered
    memoryOpen = true
  }

  async function saveMemory() {
    await apiFetch('/api/memory', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ content: memoryContent }),
    })
    memoryTampered = false
    memoryOpen = false
  }

  // The llama-server connection, persisted to config.json and hot-swapped live.
  function openLogin() {
    loginName = ''
    loginPassword = ''
    loginError = ''
    loginOpen = true
  }
  // Sign in / "become this profile": the server creates an unknown name, requires
  // a password only where the profile has one, and switches this session to that
  // account's store — so reload everything for the new identity.
  async function doLogin() {
    loginError = ''
    const name = loginName.trim()
    if (!name) { loginError = 'Enter a profile name.'; return }
    loginBusy = true
    try {
      const r = await apiFetch('/api/login', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ name, password: loginPassword }),
      })
      const data = await r.json().catch(() => ({}))
      if (!r.ok) { loginError = data.error || 'Sign in failed.'; return }
      me = data
      loginOpen = false
      activeId = null
      await refresh()
    } catch {
      loginError = 'Sign in failed.'
    } finally {
      loginBusy = false
    }
  }
  async function doLogout() {
    try { await apiFetch('/api/logout', { method: 'POST' }) } catch {}
    me = null
    activeId = null
    await refresh()
  }
  async function openSettings() {
    const s = await apiFetch('/api/settings').then((r) => r.json())
    settingsLocation = s.location ?? ''
    settingsForgeUrl = s.forgeUrl ?? ''
    settingsTaitaUrl = s.taitaUrl ?? ''
    settingsDebug = s.debug ?? false
    // Login autostart is a desktop-only, per-OS setting (the OS entry is the source
    // of truth); the browser skips it and the controls stay hidden.
    if (isDesktop) {
      try {
        const a = await apiFetch('/api/autostart').then((r) => r.json())
        autostartSupported = !!a.supported
        autostartEnabled = !!a.enabled
        autostartMinimized = !!a.minimized
      } catch {
        autostartSupported = false
      }
    }
    await loadConnections()
    await loadImageModels()
    await loadMCP()
    settingsOpen = true
  }
  // Apply an autostart change immediately (it's an OS write, independent of the
  // connection Save), reflecting back whatever state actually took.
  async function setAutostart(enabled, minimized) {
    try {
      const a = await apiFetch('/api/autostart', {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ enabled, minimized }),
      }).then((r) => r.json())
      autostartEnabled = !!a.enabled
      autostartMinimized = !!a.minimized
    } catch {
      // Leave the toggles showing the last known good state on failure.
    }
  }
  async function saveSettings() {
    await apiFetch('/api/settings', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ location: settingsLocation.trim(), forgeUrl: settingsForgeUrl.trim(), taitaUrl: settingsTaitaUrl.trim(), debug: settingsDebug }),
    })
    // The taita link may have changed — refresh the Server Chats list + flag.
    try {
      const m = await apiFetch('/api/mode').then((r) => r.json())
      serverLinked = !!m?.serverLinked
      serverChats = serverLinked ? await apiFetch('/api/server/chats').then((r) => (r.ok ? r.json() : [])).catch(() => []) : []
    } catch {}
    // Image models: persist the user's choices, or seed them from Forge on a first
    // connect (an empty list + a Forge URL). See internal/imagegen.
    if (settingsForgeUrl.trim()) {
      if (imageModels.length) await saveImageModels()
      else await fetchImageModels()
    }
    await saveMCP()
    await saveConnections()
    // "Save & connect": point the live backend at the active connection, so editing
    // its URL (or sampling) connects now — not just on the next restart. This is
    // what makes the Settings card actually switch the server, matching the button.
    // activateConnection re-polls /props itself; the checkServer below covers the
    // no-active-connection case.
    const active = connections.find(isActiveConn)
    if (active && (active.url || '').trim()) await activateConnection(active)
    settingsOpen = false
    checkServer() // re-poll so the status reflects any connection change now
  }

  // Saved llama-server connections — the Settings "Connections" card and the
  // status-bar picker. One server = one model, so switching the active connection
  // is how the user switches model: activating hot-swaps the live backend and
  // re-reads /props. See /api/connections.
  async function loadConnections() {
    try {
      const d = await apiFetch('/api/connections').then((r) => r.json())
      activeUrl = d.activeUrl ?? ''
      activeConnId = d.activeId ?? ''
      connections = (d.connections ?? []).map((c) => ({ ...c, id: c.id || newConnId(), _id: connId++ }))
    } catch {
      connections = []
    }
  }
  async function saveConnections() {
    await apiFetch('/api/connections', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ connections: connections.map((c) => ({ id: c.id, name: (c.name || '').trim(), url: (c.url || '').trim(), token: (c.token || '').trim(), temperature: numOrNull(c.temperature), minP: numOrNull(c.minP), systemPrompt: (c.systemPrompt || '').trim() })) }),
    })
  }
  function addConnection() {
    connections = [...connections, { id: newConnId(), name: '', url: '', token: '', temperature: null, minP: null, systemPrompt: '', _id: connId++ }]
  }
  function removeConnection(c) {
    connections = connections.filter((x) => x !== c)
  }
  async function activateConnection(c) {
    const url = (c.url || '').trim()
    if (!url) return
    await apiFetch('/api/connections/activate', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id: c.id, url, token: (c.token || '').trim(), temperature: numOrNull(c.temperature), minP: numOrNull(c.minP), systemPrompt: (c.systemPrompt || '').trim() }),
    })
    activeConnId = c.id
    activeUrl = url
    connOpen = false
    await checkServer() // re-read /props → model, context size, thinking support
    if (activeId) loadBudget(activeId, true) // re-meter fresh: new model tokenizes differently, and the window changed
  }

  // Image models — the Settings "Image generation" section. The list is seeded
  // from Forge (safe-by-default: heavy DiTs stay opt-in) and the user toggles
  // which may auto-select and which is the default.
  // Normalize models from the API for editing: a string form of modules (for the
  // text field) and a collapsed flag for the per-model recipe editor.
  function prepModels(arr) {
    return (arr ?? []).map((m) => ({ ...m, _modules: (m.modules || []).join(', '), _open: false }))
  }
  async function loadImageModels() {
    try {
      const d = await apiFetch('/api/imagegen').then((r) => r.json())
      imageModels = prepModels(d.models)
      imageDefault = d.default ?? ''
    } catch {
      imageModels = []
      imageDefault = ''
    }
    imageError = ''
  }
  // Fetch the checkpoints Forge currently has and merge them into the list,
  // keeping any choices already made. Uses the just-typed URL so it works before
  // Save — this is the Fetch / Refresh button and the first-connect seed.
  async function fetchImageModels() {
    if (imageFetching) return
    imageFetching = true
    imageError = ''
    try {
      const r = await apiFetch('/api/imagegen/fetch', {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ forgeUrl: settingsForgeUrl.trim() }),
      })
      if (!r.ok) throw new Error((await r.text()).trim() || `HTTP ${r.status}`)
      const d = await r.json()
      imageModels = prepModels(d.models)
      imageDefault = d.default ?? ''
    } catch (e) {
      imageError = String(e?.message || e)
    } finally {
      imageFetching = false
    }
  }
  async function saveImageModels() {
    if (!imageModels.length) return
    await apiFetch('/api/imagegen', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        default: imageDefault,
        models: imageModels.map((m) => ({
          name: m.name,
          autoPick: !!m.autoPick,
          steps: Number(m.steps) || 0,
          cfg: Number(m.cfg) || 0,
          sampler: m.sampler || '',
          scheduler: m.scheduler || '',
          width: Number(m.width) || 0,
          height: Number(m.height) || 0,
          modules: (m._modules || '').split(',').map((x) => x.trim()).filter(Boolean),
        })),
      }),
    })
  }

  // Per-chat tool curation — each chat carries its own toolset (System tools + MCP
  // servers), so a small model isn't drowned by the full set. Opened from the ⋮
  // menu; changes apply to the next message. See /api/conversations/{id}/tools.
  async function openTools() {
    if (!activeId) return
    try {
      const d = await apiFetch(`/api/conversations/${activeId}/tools`).then((r) => r.json())
      toolGroups = (d.groups ?? []).map((g) => ({ ...g, _open: g.kind === 'system' }))
    } catch {
      toolGroups = []
    }
    toolsOpen = true
  }
  async function saveTools() {
    if (!activeId) return
    const disabled = toolGroups.flatMap((g) => g.tools.filter((t) => !t.enabled).map((t) => t.name))
    await apiFetch(`/api/conversations/${activeId}/tools`, {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ disabled }),
    })
    toolsOpen = false
    loadBudget(activeId, true) // tools changed — re-meter so the new cost (and any overflow) shows now
  }
  function toggleToolGroup(g, on) {
    for (const t of g.tools) t.enabled = on
  }

  // Project surface — project/folder settings, their own place (apart from the
  // global Settings and the per-chat Tools). The system-tool defaults a new session
  // in this project inherits (still overridable per chat), plus the project's
  // read-only MCP servers. See /api/project.
  async function openProject() {
    if (!activeId) return
    try {
      const d = await apiFetch(`/api/project?conv=${activeId}`).then((r) => r.json())
      projectInProject = !!d.inProject
      projectSystem = d.systemTools ?? []
      projectMcp = d.mcp ?? []
      projectShowWorkingDir = d.showWorkingDir !== false
    } catch {
      projectInProject = false
      projectSystem = []
      projectMcp = []
      projectShowWorkingDir = true
    }
    projectOpen = true
  }
  async function saveProject() {
    if (projectInProject) {
      const disabledSystem = projectSystem.filter((t) => !t.enabled).map((t) => t.name)
      await apiFetch(`/api/project?conv=${activeId}`, {
        method: 'PUT',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ disabledSystem, showWorkingDir: projectShowWorkingDir }),
      })
    }
    projectOpen = false
  }

  // MCP servers — the Settings "MCP servers" section. Add/edit streamable-HTTP
  // servers (name, URL, Authorization header); they persist to mcp.json and
  // reconnect on the next message (no restart). stdio servers show read-only (edit
  // those in the file). Enable per chat in the Tools panel.
  function prepMCP(arr) {
    return (arr ?? []).map((s) => ({ ...s, _id: mcpId++ }))
  }
  async function loadMCP() {
    try {
      const d = await apiFetch('/api/mcp').then((r) => r.json())
      mcpServers = prepMCP(d.servers)
      mcpLoaded = true
    } catch {
      mcpServers = []
      mcpLoaded = false
    }
  }
  async function saveMCP() {
    if (!mcpLoaded) return // a failed load must not wipe the configured servers
    const servers = mcpServers
      .filter((s) => s.editable && s.name.trim() && s.url.trim())
      .map((s) => ({ name: s.name.trim(), url: s.url.trim(), header: (s.header || '').trim() }))
    await apiFetch('/api/mcp', {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ servers }),
    })
  }
  function addMCPServer() {
    mcpServers = [...mcpServers, { _id: mcpId++, name: '', url: '', header: '', transport: 'http', editable: true, connected: false }]
  }
  function removeMCPServer(sv) {
    mcpServers = mcpServers.filter((x) => x !== sv)
  }

  async function ackMemory() {
    await apiFetch('/api/memory/ack', { method: 'POST' })
    memoryTampered = false
  }

  // A tool paused for consent: the gate sent a "permission" event over the chat
  // stream (held in `pending`). The answer returns on its own POST, which
  // unblocks the waiting tool call server-side.
  async function answerPermission(allow, scope) {
    const id = pending?.id
    pending = null
    if (!id) return
    await apiFetch('/api/permission/' + id, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ allow, scope }),
    })
  }

  function scopeLabel(scope) {
    return { once: 'Allow once', session: 'Allow this chat', persistent: 'Always allow' }[scope] ?? scope
  }

  async function newChat() {
    const created = await apiFetch('/api/chats', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ title: 'New chat' }),
    }).then((r) => r.json())
    chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
    await selectChat(created.id)
  }

  async function loadSessions(projectId) {
    sessions = (await apiFetch(`/api/projects/${projectId}/sessions`).then((r) => r.json())) ?? []
  }

  // Inline rename: double-click a chat/session to edit its title in place, then
  // Enter (or blur) to persist and Escape to cancel. The title lives in the
  // conversation's meta.json; the list is reloaded so the new name shows.
  function startRename(id, title) {
    editingId = id
    editTitle = title
  }
  function cancelRename() {
    editingId = null
  }
  async function commitRename() {
    const id = editingId
    const title = editTitle.trim()
    editingId = null
    if (!id || !title) return
    await apiFetch(`/api/conversations/${id}`, {
      method: 'PUT',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ title }),
    })
    chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
    if (expandedProject) await loadSessions(expandedProject)
  }
  // Focus and select an inline-rename input as soon as it appears.
  function renameInput(node) {
    node.focus()
    node.select()
  }

  // Delete a chat/session after an inline confirm. The directory is removed and
  // the lists reload; if the active conversation went away, fall back to the
  // newest remaining one (or the empty state).
  async function doDelete(id) {
    confirmDeleteId = null
    await apiFetch(`/api/conversations/${id}`, { method: 'DELETE' })
    chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
    if (expandedProject) await loadSessions(expandedProject)
    if (activeId === id) {
      activeId = null
      messages = []
      if (chats.length) await selectChat(chats[0].id)
    }
  }

  // Projects are the store's second root; a session inside one is a full
  // conversation, selected through the same selectChat path as a free chat.
  async function toggleProject(id) {
    if (expandedProject === id) {
      expandedProject = null
      sessions = []
      return
    }
    expandedProject = id
    await loadSessions(id)
  }

  async function openFolder() {
    const path = folderPath.trim()
    if (!path) return
    const created = await apiFetch('/api/projects', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path, title: baseName(path) }),
    }).then((r) => r.json())
    folderPath = ''
    openingFolder = false
    projects = (await apiFetch('/api/projects').then((r) => r.json())) ?? []
    expandedProject = created.id
    await loadSessions(created.id)
  }

  async function newSession(projectId) {
    const created = await apiFetch(`/api/projects/${projectId}/sessions`, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ title: 'New session' }),
    }).then((r) => r.json())
    await loadSessions(projectId)
    await selectChat(created.id)
  }

  function baseName(path) {
    const parts = path.split(/[\\/]/).filter(Boolean)
    return parts.length ? parts[parts.length - 1] : path
  }

  async function send() {
    const text = input.trim()
    if (!text || streaming) return
    if (!activeId) await newChat()
    // First turn of a chat: the core auto-titles it from this message, so reload the
    // sidebar name once the turn lands (below).
    const firstMessage = messages.length === 0
    input = ''
    atBottom = true // sending jumps to the newest message
    const now = new Date().toISOString()
    messages.push({ role: 'user', content: text, time: now })
    messages.push({ role: 'assistant', content: '', reasoning: '', tools: [], time: now })
    streaming = true
    toolRunning = false
    streamingChatId = activeId // bind this stream to its chat (see handleEvent / selectChat)
    streamMessages = messages // the array this stream writes to, even if the view switches away
    try {
      // The client sends only the new turn; the core persists both sides.
      await streamChat(activeId, text)
      if (firstMessage) {
        // The core named this chat from its first message — reload so the sidebar shows it.
        chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
        if (expandedProject) await loadSessions(expandedProject)
      }
    } catch (e) {
      error = String(e)
      reachable = false
    } finally {
      streaming = false
      toolRunning = false
      streamingChatId = null
      streamMessages = null
    }
  }

  async function streamChat(id, content) {
    const resp = await apiFetch('/api/chat/stream', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id, content, thinking: conn?.thinkingSupported ? thinking : undefined }),
    })
    if (!resp.ok) throw new Error('stream request failed')
    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let i
      while ((i = buf.indexOf('\n\n')) >= 0) {
        handleEvent(buf.slice(0, i))
        buf = buf.slice(i + 2)
      }
    }
  }

  function handleEvent(raw) {
    let event = 'message'
    let data = ''
    for (const line of raw.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim()
      else if (line.startsWith('data:')) data += line.slice(5).trim()
    }
    if (!data) return
    const payload = JSON.parse(data)
    // The stream writes to its OWN chat's message array (streamMessages), never
    // whichever chat is on screen — so switching chats mid-stream can't corrupt
    // another chat, and switching back shows the progress. Chat-level state
    // (budget, prompts, errors) updates only while that chat is the one in view.
    const live = streamMessages
    const onScreen = streamingChatId === activeId
    if (event === 'delta' && payload.content) {
      toolRunning = false // content is streaming again — no longer waiting on a tool
      if (live?.length) live[live.length - 1].content += payload.content
    } else if (event === 'reasoning' && payload.content) {
      if (live?.length) live[live.length - 1].reasoning += payload.content
    } else if (event === 'tool_call') {
      toolRunning = true // a tool is now running — keep the working indicator up through the gap
      const last = live?.[live.length - 1]
      if (last) (last.tools ??= []).push({ name: payload.name, arguments: payload.arguments })
    } else if (event === 'compacted') {
      if (onScreen) {
        report = `Auto-compacted: folded ${payload.folded}, saved ~${payload.saved} tokens`
        if (payload.summary) convSummaries = [...convSummaries, { content: payload.summary, time: new Date().toISOString(), afterIndex: messages.length - 1, saved: payload.saved }]
      }
    } else if (event === 'permission') {
      pending = payload // always surface: a blocking approval, shown whichever chat is active (answered by id)
    } else if (event === 'done') {
      toolRunning = false
      pending = null // the turn resolved — dismiss any consent prompt that went unanswered
      // Tag the turn's assistant bubble with its generation speed, so tok/s rides
      // the response (beside its timestamp), not one global status-bar number. Done
      // regardless of which chat is on screen, so a backgrounded turn keeps it too.
      const tps = payload.timings?.predictPerSecond
      if (tps && live?.length) {
        const last = live[live.length - 1]
        if (last?.role === 'assistant') last.tokPerSec = tps
      }
      if (onScreen) {
        budget = payload.budget
        reachable = true
      }
      reconcileFinal(streamingChatId, live)
    } else if (event === 'error') {
      pending = null
      error = payload.error // surface globally, so a backgrounded turn's failure isn't silent
    }
  }

  // On turn completion, replace the streamed assistant bubble with the saved
  // copy from disk. The server post-processes the reply before saving (drops the
  // ReAct scratch blob, recovers an image the model generated but didn't echo,
  // rewrites remote image links to local attachments), and writes it before it
  // emits `done` — so the saved copy is authoritative and the final render always
  // matches what's on disk, even when the model streamed nothing (as happens
  // right after a silent image generation). The live token stream still shows
  // text as it arrives; this only reconciles the end state.
  async function reconcileFinal(id, arr) {
    if (!id || !arr) return
    try {
      const entries = (await apiFetch(`/api/conversations/${id}`).then((r) => r.json())) ?? []
      let lastContent = null
      for (const e of entries) {
        if (e.kind === 'msg' && e.role === 'assistant') lastContent = e.content
      }
      if (lastContent === null) return
      const last = arr[arr.length - 1]
      if (last && last.role === 'assistant') last.content = lastContent
    } catch {
      /* leave the streamed bubble as-is if the reconcile fetch fails */
    }
  }

  // ISO-ish local timestamp ("2026-10-28 15:42") — numeric, not "Wednesday".
  function fmtTime(ts) {
    if (!ts) return ''
    try {
      return new Date(ts).toLocaleString('sv-SE').slice(0, 16)
    } catch {
      return ''
    }
  }

  // Copy a message's raw markdown; flash "copied" on its button briefly.
  function copyMsg(text, i) {
    navigator.clipboard?.writeText(text)
    copiedAt = i
    setTimeout(() => { if (copiedAt === i) copiedAt = null }, 1200)
  }

  // Svelte action: give each fenced code block in rendered markdown its own copy
  // button. Re-runs when the content changes (streaming); the guard avoids a
  // duplicate button.
  function copyableCode(node) {
    const enhance = () => {
      for (const pre of node.querySelectorAll('pre')) {
        if (pre.querySelector('.codecopy')) continue
        const btn = document.createElement('button')
        btn.type = 'button'
        btn.className = 'codecopy'
        btn.textContent = 'copy'
        btn.addEventListener('click', () => {
          navigator.clipboard?.writeText((pre.querySelector('code') ?? pre).textContent ?? '')
          btn.textContent = 'copied'
          setTimeout(() => { btn.textContent = 'copy' }, 1200)
        })
        pre.appendChild(btn)
      }
    }
    enhance()
    return { update: enhance }
  }

  // Sidebar collapse state, persisted per browser (lean localStorage; defaults
  // to shown if storage is unavailable).
  function loadSidebar() {
    try {
      return localStorage.getItem('llamingo.sidebar') !== 'closed'
    } catch {
      return true
    }
  }
  function toggleSidebar() {
    sidebarOpen = !sidebarOpen
    try {
      localStorage.setItem('llamingo.sidebar', sidebarOpen ? 'open' : 'closed')
    } catch {}
  }
  // Detailed vs chat-only reading: whether to show the assistant's thinking trace
  // and "used X" tool chips. View-only and per browser — the trail is still saved
  // to disk and still re-enters nothing; this only hides it. Defaults to shown.
  function loadShowDetail() {
    try {
      return localStorage.getItem('llamingo.showDetail') !== 'off'
    } catch {
      return true
    }
  }
  function setShowDetail(v) {
    showDetail = v
    try {
      localStorage.setItem('llamingo.showDetail', v ? 'on' : 'off')
    } catch {}
  }
  // The hamburger toggles the saved sidebar when wide, but only the transient
  // drawer when thin — so toggling on a phone never rewrites the saved preference.
  function toggleNav() {
    if (narrow) drawerOpen = !drawerOpen
    else toggleSidebar()
  }
  // Swipe to reveal / dismiss the drawer on touch (the toggle covers mouse users).
  let touchStartX = 0, touchStartY = 0
  function onTouchStart(e) {
    const t = e.touches[0]
    touchStartX = t.clientX
    touchStartY = t.clientY
  }
  function onTouchEnd(e) {
    if (!narrow) return
    const t = e.changedTouches[0]
    const dx = t.clientX - touchStartX
    const dy = t.clientY - touchStartY
    if (Math.abs(dx) < 50 || Math.abs(dy) > Math.abs(dx)) return // ignore taps and vertical scrolls
    if (dx > 0 && touchStartX < 30 && !drawerOpen) drawerOpen = true // swipe in from the left edge
    else if (dx < 0 && drawerOpen) drawerOpen = false // swipe the drawer away
  }
  function loadSidebarWidth() {
    try {
      const v = parseInt(localStorage.getItem('llamingo.sidebarWidth') ?? '', 10)
      if (Number.isFinite(v)) return Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, v))
    } catch {}
    return 240
  }
  // Drag the divider to resize the sidebar; clamp so the conversation keeps room,
  // and persist the final width per browser.
  function startResize(e) {
    e.preventDefault()
    const startX = e.clientX
    const startW = sidebarWidth
    const max = Math.min(SIDEBAR_MAX, window.innerWidth - 360)
    const onMove = (ev) => {
      sidebarWidth = Math.max(SIDEBAR_MIN, Math.min(max, startW + ev.clientX - startX))
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      try {
        localStorage.setItem('llamingo.sidebarWidth', String(Math.round(sidebarWidth)))
      } catch {}
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
  }

  function modelName(path) {
    if (!path) return 'unknown model'
    return path.split(/[\\/]/).pop().replace(/\.gguf$/, '')
  }

  // Meter the budget against whatever connection is active NOW: switching to a
  // smaller-context model re-evaluates the same token count against the new window
  // (and can tip it into overflow) without waiting for the next turn. conn.nCtx
  // updates on every /props poll, including right after a connection switch.
  const ctxMax = $derived(conn?.nCtx || budget?.nCtx || 0)
  // The active connection's name (alias), for the status bar. Empty until the
  // connections load (on mount / picker open), then "Alias (model)" shows.
  const activeConnName = $derived(connections.find((c) => c.id === activeConnId)?.name ?? '')
  const pct = $derived(budget && ctxMax ? Math.min(100, Math.round((100 * budget.used) / ctxMax)) : 0)
  const ctxOver = $derived(!!(budget && ctxMax && budget.used > ctxMax))
  const ctxLevel = $derived(
    !budget || !ctxMax ? 'healthy' : budget.used / ctxMax >= 0.9 ? 'full' : budget.used / ctxMax >= 0.75 ? 'near' : 'healthy'
  )

  // The window title reflects the active conversation — "llaminGO › Chat name" in
  // the desktop title bar / taskbar and the browser tab, or just "llaminGO" at the
  // chat list. The title comes from the loaded chat/session lists.
  const activeTitle = $derived(
    chats.find((c) => c.id === activeId)?.title ?? sessions.find((s) => s.id === activeId)?.title ?? ''
  )
  $effect(() => {
    const t = activeTitle ? `llaminGO › ${activeTitle}` : 'llaminGO'
    document.title = t
    if (isDesktop) window.runtime?.WindowSetTitle?.(t)
  })
  // Know eagerly whether the active chat is a project session, so the status bar
  // can gate its Project control without opening the surface. Uses the cheap
  // /status probe (no MCP), and discards a stale answer on a fast chat switch.
  $effect(() => {
    const id = activeId
    if (!id) { projectInProject = false; return }
    let cancelled = false
    apiFetch(`/api/project/status?conv=${id}`)
      .then((r) => r.json())
      .then((d) => { if (!cancelled) projectInProject = !!d.inProject })
      .catch(() => { if (!cancelled) projectInProject = false })
    return () => { cancelled = true }
  })
</script>

<div class="app">
  <header
    class="topbar"
    ondblclick={(e) => {
      if (isDesktop && !e.target.closest('button') && !e.target.closest('.winctl')) window.runtime?.WindowToggleMaximise?.()
    }}
  >
    {#if isDesktop}
      <button class="mark-btn" aria-label="Window menu" onclick={() => (sysMenu = !sysMenu)}>
        <img class="mark" src="/favicon.svg" alt="" width="24" height="24" />
      </button>
    {:else}
      <img class="mark" src="/favicon.svg" alt="" width="24" height="24" />
    {/if}
    <button
      class="icon-btn"
      aria-label={sidebarShown ? 'Hide sidebar' : 'Show sidebar'}
      title={sidebarShown ? 'Hide sidebar' : 'Show sidebar'}
      onclick={toggleNav}
    >
      <svg width="18" height="18" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <line x1="2" y1="4.5" x2="10" y2="4.5" />
        <line x1="2" y1="8" x2="10" y2="8" />
        <line x1="2" y1="11.5" x2="10" y2="11.5" />
        {#if sidebarShown}
          <polyline points="14.5,5.5 12,8 14.5,10.5" />
        {:else}
          <polyline points="12,5.5 14.5,8 12,10.5" />
        {/if}
      </svg>
    </button>
    <span class="wordmark">llaminGO{activeTitle ? ` › ${activeTitle}` : ''}</span>
    <span class="spacer"></span>
    {#if !narrow}
    <button
      class="icon-btn"
      aria-label="Menu"
      aria-haspopup="true"
      aria-expanded={actionsMenu}
      title="Memory, Settings"
      onclick={() => (actionsMenu = !actionsMenu)}
    >
      <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true">
        <circle cx="8" cy="3" r="1.5" /><circle cx="8" cy="8" r="1.5" /><circle cx="8" cy="13" r="1.5" />
      </svg>
    </button>
    {/if}
    {#if isDesktop}
      <div class="winctl">
        <button class="winbtn" aria-label="Minimize" onclick={() => window.runtime?.WindowMinimise?.()}>
          <svg width="11" height="11" viewBox="0 0 11 11"><line x1="1" y1="6" x2="10" y2="6" stroke="currentColor" stroke-width="1" /></svg>
        </button>
        <button class="winbtn" aria-label={maximized ? 'Restore' : 'Maximize'} onclick={() => window.runtime?.WindowToggleMaximise?.()}>
          {#if maximized}
            <svg width="11" height="11" viewBox="0 0 11 11"><rect x="1.5" y="3.5" width="6" height="6" fill="none" stroke="currentColor" stroke-width="1" /><path d="M3.5 3.5 V1.5 H9.5 V7.5 H7.5" fill="none" stroke="currentColor" stroke-width="1" /></svg>
          {:else}
            <svg width="11" height="11" viewBox="0 0 11 11"><rect x="1.5" y="1.5" width="8" height="8" fill="none" stroke="currentColor" stroke-width="1" /></svg>
          {/if}
        </button>
        <button class="winbtn close" aria-label="Close" onclick={() => window.runtime?.Quit?.()}>
          <svg width="11" height="11" viewBox="0 0 11 11"><line x1="1.5" y1="1.5" x2="9.5" y2="9.5" stroke="currentColor" stroke-width="1.2" /><line x1="9.5" y1="1.5" x2="1.5" y2="9.5" stroke="currentColor" stroke-width="1.2" /></svg>
        </button>
      </div>
    {/if}
  </header>

  {#if isDesktop && sysMenu}
    <div class="sysmenu-backdrop" onclick={() => (sysMenu = false)}></div>
    <nav class="sysmenu">
      <button onclick={() => { window.runtime?.WindowMinimise?.(); sysMenu = false }}>Minimize</button>
      <button onclick={() => { window.runtime?.WindowToggleMaximise?.(); sysMenu = false }}>Maximize / Restore</button>
      <button onclick={() => { onTop = !onTop; window.runtime?.WindowSetAlwaysOnTop?.(onTop); sysMenu = false }}>{onTop ? '✓ ' : ''}Always on top</button>
      <button class="danger" onclick={() => { window.runtime?.EventsEmit?.('app:quit'); sysMenu = false }}>Close llaminGO</button>
    </nav>
  {/if}

  {#if actionsMenu}
    <div class="sysmenu-backdrop" onclick={() => (actionsMenu = false)}></div>
    <nav class="sysmenu actionsmenu" class:up={narrow}>
      <button onclick={() => { actionsMenu = false; openMemory() }}>Memory</button>
      {#if !serverMode || me?.admin}
        <button onclick={() => { actionsMenu = false; openSettings() }}>Settings</button>
      {/if}
    </nav>
  {/if}

  {#if staleNote}
    <button class="notice" onclick={() => (staleNote = null)}>{staleNote} · dismiss</button>
  {/if}

  <div class="body" class:narrow={narrow} ontouchstart={onTouchStart} ontouchend={onTouchEnd}>
    {#if narrow && drawerOpen}
      <div class="drawer-scrim" onclick={() => (drawerOpen = false)}></div>
    {/if}
    <aside class="left" class:collapsed={!sidebarShown} style="--sidebar-w:{sidebarWidth}px; margin-left:{sidebarShown ? '0px' : -sidebarWidth + 'px'}">
      {#if serverMode}
        <div class="profile">
          {#if me?.user}
            <span class="profile-who" title={me.admin ? 'Admin' : 'Signed in'}>{me.admin ? '★ ' : ''}{me.user}</span>
            <button class="profile-btn" onclick={doLogout}>Sign out</button>
          {:else}
            <button class="profile-btn signin" onclick={openLogin}>Sign in</button>
          {/if}
        </div>
      {/if}
      <div class="switcher">{serverLinked && !serverMode ? 'Local Chats' : 'Chats'}</div>
      <button class="new" onclick={newChat}>+ New chat</button>
      <ul class="list">
        {#each chats as c}
          <li class="rowwrap">
            {#if editingId === c.id}
              <input
                class="field rename"
                bind:value={editTitle}
                use:renameInput
                onkeydown={(e) => { if (e.key === 'Enter') commitRename(); else if (e.key === 'Escape') cancelRename() }}
                onblur={commitRename}
              />
            {:else if confirmDeleteId === c.id}
              <span class="confirmdel">Delete this chat?</span>
              <button class="del-yes" onclick={() => doDelete(c.id)}>Delete</button>
              <button class="del-no" onclick={() => (confirmDeleteId = null)}>Cancel</button>
            {:else}
              <button
                class="row"
                class:active={c.id === activeId}
                onclick={() => selectChat(c.id)}
                ondblclick={() => startRename(c.id, c.title)}
                title="Double-click to rename"
              >
                {c.title || 'Untitled'}
              </button>
              <button class="del" aria-label="Delete chat" title="Delete chat" onclick={() => (confirmDeleteId = c.id)}>✕</button>
            {/if}
          </li>
        {:else}
          <li class="row muted">No chats yet</li>
        {/each}
      </ul>

      {#if serverLinked && !serverMode}
      <div class="switcher">Server Chats</div>
      <ul class="list">
        {#each serverChats as c}
          <li class="rowwrap">
            <button class="row" class:active={c.id === activeId} onclick={() => selectChat(c.id)}>
              {c.title || 'Untitled'}
            </button>
          </li>
        {:else}
          <li class="row muted">No server chats yet</li>
        {/each}
      </ul>
      {/if}

      {#if !mobileClient && !serverMode}
      <div class="switcher">Projects</div>
      <button class="new" onclick={() => (openingFolder = !openingFolder)}>+ Open folder</button>
      {#if openingFolder}
        <input
          class="field path"
          placeholder="/path/to/folder"
          bind:value={folderPath}
          onkeydown={(e) => e.key === 'Enter' && openFolder()}
        />
      {/if}
      <ul class="list">
        {#each projects as p}
          <li>
            <button class="row" class:active={p.id === expandedProject} onclick={() => toggleProject(p.id)}>
              {p.title || 'Untitled'}
            </button>
            {#if expandedProject === p.id}
              <ul class="sublist">
                {#each sessions as s}
                  <li class="rowwrap">
                    {#if editingId === s.id}
                      <input
                        class="field rename sub"
                        bind:value={editTitle}
                        use:renameInput
                        onkeydown={(e) => { if (e.key === 'Enter') commitRename(); else if (e.key === 'Escape') cancelRename() }}
                        onblur={commitRename}
                      />
                    {:else if confirmDeleteId === s.id}
                      <span class="confirmdel">Delete this session?</span>
                      <button class="del-yes" onclick={() => doDelete(s.id)}>Delete</button>
                      <button class="del-no" onclick={() => (confirmDeleteId = null)}>Cancel</button>
                    {:else}
                      <button
                        class="row sub"
                        class:active={s.id === activeId}
                        onclick={() => selectChat(s.id)}
                        ondblclick={() => startRename(s.id, s.title)}
                        title="Double-click to rename"
                      >
                        {s.title || 'Untitled'}
                      </button>
                      <button class="del" aria-label="Delete session" title="Delete session" onclick={() => (confirmDeleteId = s.id)}>✕</button>
                    {/if}
                  </li>
                {:else}
                  <li class="row sub muted">No sessions yet</li>
                {/each}
                <li>
                  <button class="row sub add" onclick={() => newSession(p.id)}>+ New session</button>
                </li>
              </ul>
            {/if}
          </li>
        {:else}
          <li class="row muted">No projects yet</li>
        {/each}
      </ul>
      {/if}
    </aside>
    {#if !narrow && sidebarShown}
      <div
        class="resizer"
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize sidebar"
        onpointerdown={startResize}
      ></div>
    {/if}

    <main class="conversation">
      <div class="thread" bind:this={threadEl} onscroll={onThreadScroll}>
        {#if error}<p class="error">{error}</p>{/if}
        {#each messages as m, i}
          {#if showDetail && m.reasoning}
            <details class="thinking" open={streaming && i === messages.length - 1 && !m.content}>
              <summary>Thinking</summary>
              <div class="rtext prose">{@html md.render(m.reasoning)}</div>
            </details>
          {/if}
          {#if m.role === 'user'}
            <div class="msg user">{m.content}</div>
          {:else}
            {#if showDetail && m.tools?.length}
              {#each m.tools as t}
                <div class="toolcall" title={t.arguments}>used <strong>{t.name}</strong></div>
              {/each}
            {/if}
            {#if m.content !== ''}
              <div class="msg assistant prose" use:copyableCode={m.content}>{@html renderContent(m.content)}</div>
            {/if}
            {#if streaming && i === messages.length - 1 && (m.content === '' || toolRunning)}
              <div class="msg assistant" style="opacity:.6">{SPINNER[spinnerFrame]} {workingLabel(m)}…</div>
            {/if}
          {/if}
          {#if m.time && (m.role === 'user' || m.content !== '')}
            <div class="meta" class:mine={m.role === 'user'}>
              <button class="metabtn" onclick={() => copyMsg(m.content, i)}>{copiedAt === i ? 'copied' : 'copy'}</button>
              <span>{fmtTime(m.time)}</span>
              {#if m.tokPerSec}<span title="generation tokens per second">· {Math.round(m.tokPerSec)} tok/s</span>{/if}
            </div>
          {/if}
          {#each convSummaries.filter((s) => s.afterIndex === i) as s}
            <details class="thinking summary-mark">
              <summary>Compacted · {fmtTime(s.time)}{#if s.saved} · saved ~{s.saved} tokens{/if}</summary>
              <div class="rtext prose">{@html md.render(s.content)}</div>
            </details>
          {/each}
        {:else}
          <p class="empty muted">Ask something to get started.</p>
        {/each}
        {#if compacting}
          <div class="compacting muted">Compacting…</div>
        {/if}
        {#if !atBottom && messages.length}
          <button class="jumpdown" onclick={scrollToLatest} title="Jump to the latest message">↓ Latest</button>
        {/if}
      </div>
      {#if pending}
        <div class="consent">
          <div class="consent-head">
            Permission needed
            <span class="risk {pending.risk}">{pending.risk} risk</span>
          </div>
          <div class="consent-label">{pending.label}</div>
          {#if pending.detail}<pre class="consent-detail">{pending.detail}</pre>{/if}
          <div class="consent-actions">
            {#each pending.scopes as scope}
              <button class="primary" onclick={() => answerPermission(true, scope)}>{scopeLabel(scope)}</button>
            {/each}
            <button onclick={() => answerPermission(false, '')}>Deny</button>
          </div>
        </div>
      {/if}
      <div class="composer">
        <textarea
          class="field composer-input"
          rows="1"
          placeholder="Ask something…"
          bind:value={input}
          bind:this={inputEl}
          onkeydown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.isComposing && !coarsePointer) { e.preventDefault(); send() } }}
          disabled={streaming}
        ></textarea>
        <div class="composer-controls">
          {#if conn?.thinkingSupported}
            <button class="pill-btn" class:on={thinking} onclick={() => (thinking = !thinking)} title="Toggle the model's thinking for new messages">Thinking {thinking ? 'on' : 'off'}</button>
          {/if}
          <button class="pill-btn" onclick={openTools} disabled={!activeId} title="Trim the tools for this chat">Tools</button>
          <span class="spacer"></span>
          <button class="pill-btn send-btn" onclick={send} disabled={streaming || !input.trim()} aria-label="Send" title="Send (Enter)">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19V5" /><path d="M5 12l7-7 7 7" /></svg>
          </button>
        </div>
      </div>

      <footer class="statusbar" class:narrow={narrow}>
    {#if narrow}
      <button class="statusbtn icon" onclick={toggleNav} aria-label="Chats" title="Chats">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" aria-hidden="true">
          <line x1="3" y1="4.5" x2="13" y2="4.5" /><line x1="3" y1="8" x2="13" y2="8" /><line x1="3" y1="11.5" x2="13" y2="11.5" />
        </svg>
      </button>
    {/if}
    <!-- The connection picker stays available even when the active server is
         unreachable, so a dead connection never traps the user — they can still
         open this and switch to a live one (or reach Manage… / Settings). The
         state shows in the label and the dot rather than hiding the control. -->
    <div class="connwrap">
      <button class="statusmodel connpick" onclick={() => { connOpen = !connOpen; if (connOpen) loadConnections() }} aria-haspopup="true" aria-expanded={connOpen} title="Switch connection">
        <span class="dot" class:off={!reachable}></span>
        <span class="connpick-label">{#if conn && reachable}{activeConnName ? `${activeConnName} (${modelName(conn.model)})` : `${modelName(conn.model)} · ${conn.nCtx} ctx`}{:else if !reachable}server unreachable — reconnecting…{:else if error}couldn't reach the server{:else}connecting…{/if}</span>
        <svg class="caret" width="9" height="9" viewBox="0 0 10 10" fill="none" stroke="currentColor" stroke-width="1.3" aria-hidden="true"><path d="M2.5 4 L5 6.5 L7.5 4" /></svg>
      </button>
      {#if connOpen}
        <div class="conn-backdrop" onclick={() => (connOpen = false)}></div>
        <div class="conn-pop">
          {#each connections as c (c._id ?? c.url)}
            <button class="conn-row" class:active={isActiveConn(c)} onclick={() => activateConnection(c)}>
              <span class="conn-dot" class:on={isActiveConn(c)}></span>
              <span class="conn-row-name">{c.name}</span>
              <span class="conn-row-url muted">{c.url}</span>
              {#if c.temperature != null}<span class="conn-temp muted">temp {c.temperature}</span>{/if}
            </button>
          {/each}
          <button class="conn-manage" onclick={() => { connOpen = false; openSettings() }}>Manage…</button>
        </div>
      {/if}
    </div>
    {#if projectInProject && !narrow && !mobileClient}
      <button class="statusbtn" onclick={openProject} aria-label="Project" title="Project settings">Project</button>
    {/if}
    <span class="spacer"></span>
    {#if report}<span class="report">{report}</span>{/if}
    {#if projectInProject && narrow && !mobileClient}
      <button class="statusbtn icon" onclick={openProject} aria-label="Project" title="Project"><svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round" aria-hidden="true"><path d="M2 4.5a1 1 0 0 1 1-1h3l1.5 1.5h6a1 1 0 0 1 1 1V12a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V4.5z" /></svg></button>
    {/if}
    {#if budget && activeId}
      <div class="ctxwrap">
        <button class="statusbtn ctx-pill {ctxLevel}" onclick={() => (budgetOpen = !budgetOpen)} aria-haspopup="true" aria-expanded={budgetOpen} title={ctxOver ? 'Over the context window' : 'Context usage'}>
          <svg class="batt" viewBox="0 0 24 12" width="22" height="11" aria-hidden="true">
            <rect x="1" y="1.5" width="18" height="9" rx="2" fill="none" stroke="currentColor" stroke-width="1.2" />
            <rect x="20.5" y="4" width="2.5" height="4" rx="1" fill="currentColor" />
            <rect x="2.6" y="3.1" width={Math.max(0, Math.min(15.8, (pct / 100) * 15.8))} height="5.8" rx="1" fill="currentColor" />
          </svg>
          <span class="ctx-pct">{pct}%</span>
          {#if ctxOver}<span class="ctx-bang" aria-label="over the context window">!</span>{/if}
          {#if !narrow}<span class="ctx-label">Context</span>{/if}
        </button>
        {#if budgetOpen}
          <div class="ctx-backdrop" onclick={() => (budgetOpen = false)}></div>
          <div class="ctx-pop">
            <div class="bar {ctxLevel}"><span class="fill" style="width:{pct}%"></span></div>
            <div class="ctx-count">{budget.used} / {ctxMax} tokens</div>
            {#if ctxOver}
              <div class="ctx-warn">Over this model's {ctxMax}-token window. Trim tools (the Tools button) or switch to a larger-context model.</div>
            {:else if ctxLevel === 'near'}
              <div class="ctx-warn soft">Near this model's {ctxMax}-token window.</div>
            {/if}
            <button class="statusbtn" onclick={() => { budgetOpen = false; compact() }} disabled={streaming}>Compact</button>
          </div>
        {/if}
      </div>
    {/if}
    {#if narrow}
      <button class="statusbtn icon" aria-label="Menu" aria-haspopup="true" aria-expanded={actionsMenu} title="Memory, Settings" onclick={() => (actionsMenu = !actionsMenu)}>
        <svg width="16" height="16" viewBox="0 0 16 16" fill="currentColor" aria-hidden="true"><circle cx="8" cy="3" r="1.5" /><circle cx="8" cy="8" r="1.5" /><circle cx="8" cy="13" r="1.5" /></svg>
      </button>
    {/if}
      </footer>
    </main>
  </div>

  {#if memoryOpen}
    <div class="modal-backdrop" onclick={() => (memoryOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <span>Memory</span>
          <button class="x" onclick={() => (memoryOpen = false)}>✕</button>
        </div>
        {#if memoryTampered}
          <div class="warn">Edited outside llaminGO. Review it, then Save to keep it or Dismiss the warning.</div>
        {/if}
        <textarea class="memo" bind:value={memoryContent} placeholder="Personal memory the model always sees…"></textarea>
        <div class="modal-foot">
          {#if memoryTampered}<button onclick={ackMemory}>Dismiss</button>{/if}
          <button class="primary" onclick={saveMemory}>Save</button>
        </div>
      </div>
    </div>
  {/if}

  {#if toolsOpen}
    <div class="modal-backdrop" onclick={() => (toolsOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <span>Tools</span>
          <button class="x" onclick={() => (toolsOpen = false)}>✕</button>
        </div>
        <div class="modal-body">
          {#if toolGroups.length}
            <div class="imgmodels-hint muted">Trim the toolset for this chat — a small model does better with fewer tools. Changes apply to the next message.</div>
            {#each toolGroups as g (g.kind + ':' + g.name)}
              <div class="toolgroup">
                <div class="toolgroup-head">
                  {#if g.kind === 'mcp'}
                    <button class="imgmodel-exp" title="Show tools" onclick={() => (g._open = !g._open)}>{g._open ? '▾' : '▸'}</button>
                  {/if}
                  <label class="toolgroup-name">
                    <input
                      type="checkbox"
                      checked={g.tools.every((t) => t.enabled)}
                      indeterminate={g.tools.some((t) => t.enabled) && !g.tools.every((t) => t.enabled)}
                      onchange={(e) => toggleToolGroup(g, e.currentTarget.checked)}
                    />
                    {g.kind === 'mcp' ? g.name : 'System tools'}{#if g.scope === 'project'} <span class="muted">· project</span>{/if}
                    <span class="muted">({g.tools.filter((t) => t.enabled).length}/{g.tools.length})</span>
                  </label>
                </div>
                {#if g.kind === 'system' || g._open}
                  <ul class="toolgroup-list">
                    {#each g.tools as t (t.name)}
                      <label class="toolrow" title={t.description || ''}>
                        <input type="checkbox" bind:checked={t.enabled} />
                        <span class="toolname">{t.name}</span>
                      </label>
                    {/each}
                  </ul>
                {/if}
              </div>
            {/each}
          {:else}
            <div class="imgmodels-hint muted">No tools available for this chat.</div>
          {/if}
        </div>
        <div class="modal-foot">
          <button class="primary" onclick={saveTools}>Save</button>
        </div>
      </div>
    </div>
  {/if}

  {#if projectOpen}
    <div class="modal-backdrop" onclick={() => (projectOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <span>Project</span>
          <button class="x" onclick={() => (projectOpen = false)}>✕</button>
        </div>
        <div class="modal-body">
          {#if !projectInProject}
            <div class="imgmodels-hint muted">This chat isn't in a project folder. Open a folder and start a session in it to set its tool defaults.</div>
          {:else}
            <div class="group-head">System tool defaults <span class="muted">(what a new session here starts with)</span></div>
            <div class="toollist">
              {#each projectSystem as t (t.name)}
                <label class="toolrow" title={t.description || ''}>
                  <input type="checkbox" bind:checked={t.enabled} />
                  <span class="toolname">{t.name}</span>
                </label>
              {/each}
            </div>

            <div class="group-head">Working directory <span class="muted">(folder awareness in the prompt)</span></div>
            <div class="toollist">
              <label class="toolrow" title="Prepend a short note telling the model its working directory, so it refers to files by paths relative to it. Counts toward the context budget.">
                <input type="checkbox" bind:checked={projectShowWorkingDir} />
                <span class="toolname">Tell the model its working directory</span>
              </label>
            </div>

            <div class="group-head">MCP servers <span class="muted">(this project's .mcp.json — read-only)</span></div>
            {#if projectMcp.length}
              <div class="imgmodels">
                <ul class="imgmodels-list">
                  {#each projectMcp as sv (sv.name)}
                    <li class="mcpserver">
                      <div class="mcpserver-head">
                        <span class="conn-dot" class:on={sv.connected} title={sv.connected ? 'connected' : 'not connected'}></span>
                        <span class="mcp-name-ro">{sv.name}</span>
                        <span class="muted">{sv.transport}</span>
                      </div>
                    </li>
                  {/each}
                </ul>
              </div>
            {:else}
              <div class="imgmodels-hint muted">No project MCP servers. Add a <code>.mcp.json</code> to the folder.</div>
            {/if}
          {/if}
        </div>
        <div class="modal-foot">
          <button class="primary" onclick={saveProject}>Save</button>
        </div>
      </div>
    </div>
  {/if}

  {#if loginOpen}
    <div class="modal-backdrop" onclick={() => (loginOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <span>Sign in</span>
          <button class="x" onclick={() => (loginOpen = false)}>✕</button>
        </div>
        <div class="modal-body">
          <label class="setting">
            <span>Profile name</span>
            <input class="field" bind:value={loginName} placeholder="e.g. Dad" onkeydown={(e) => e.key === 'Enter' && doLogin()} />
          </label>
          <label class="setting">
            <span>Password <span class="muted">(optional for a new profile; required if it has one)</span></span>
            <input class="field" type="password" bind:value={loginPassword} onkeydown={(e) => e.key === 'Enter' && doLogin()} />
          </label>
          <div class="muted">A new name creates a profile. The first profile becomes the admin and needs a password.</div>
          {#if loginError}<div class="login-err">{loginError}</div>{/if}
        </div>
        <div class="modal-foot">
          <button class="primary" onclick={doLogin} disabled={loginBusy}>{loginBusy ? 'Signing in…' : 'Sign in'}</button>
        </div>
      </div>
    </div>
  {/if}

  {#if settingsOpen}
    <div class="modal-backdrop" onclick={() => (settingsOpen = false)}>
      <div class="modal" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <span>Settings</span>
          <button class="x" onclick={() => (settingsOpen = false)}>✕</button>
        </div>
        <div class="modal-body">
        <div class="group-head">Connections <span class="muted">(llama-servers to switch between)</span></div>
        <div class="imgmodels">
          <div class="imgmodels-head">
            <span class="muted">Servers{connections.length ? ` · ${connections.length}` : ''}</span>
            <button class="refresh" onclick={addConnection}>+ Add connection</button>
          </div>
          {#if connections.length}
            <div class="imgmodels-hint muted">One server serves one model, so the active connection is your model. Set temp / min_p to turn one server into personas — a low-temp "Coder", a high-temp "Creative"; blank = the model's default. Pick the active one (the dot) here or from the status bar.</div>
            <ul class="imgmodels-list">
              {#each connections as c (c._id)}
                <li class="mcpserver">
                  <div class="mcpserver-head">
                    <label class="conn-radio" title={isActiveConn(c) ? 'Active connection' : 'Make this the active connection'}>
                      <input type="radio" name="activeconn" checked={isActiveConn(c)} onchange={() => activateConnection(c)} />
                    </label>
                    <input class="field mcp-name" placeholder="name" bind:value={c.name} />
                    <button class="imgmodel-exp" title="Remove connection" onclick={() => removeConnection(c)}>✕</button>
                  </div>
                  <input class="field" placeholder="http://localhost:8080" bind:value={c.url} />
                  <input class="field" type="password" placeholder="Bearer token — optional (for an auth proxy)" bind:value={c.token} />
                  <div class="conn-sampling">
                    <label>temp <input class="field conn-num" type="number" step="0.1" min="0" max="2" placeholder="default" bind:value={c.temperature} /></label>
                    <label>min_p <input class="field conn-num" type="number" step="0.01" min="0" max="1" placeholder="default" bind:value={c.minP} /></label>
                  </div>
                  <textarea class="field conn-prompt" rows="2" placeholder="System prompt — optional (this persona's instructions)" bind:value={c.systemPrompt}></textarea>
                </li>
              {/each}
            </ul>
          {:else}
            <div class="imgmodels-hint muted">No saved connections. Add one, or it falls back to $LLAMINGO_URL.</div>
          {/if}
        </div>

        {#if !serverMode}
        <div class="group-head">Server (Taita)</div>
        <label class="setting">
          <span>Taita server URL <span class="muted">(optional; shows that server's chats under "Server Chats")</span></span>
          <input
            class="field"
            bind:value={settingsTaitaUrl}
            placeholder="https://taita.example.com"
            onkeydown={(e) => e.key === 'Enter' && saveSettings()}
          />
        </label>
        {/if}

        <div class="group-head">Image generation</div>
        <label class="setting">
          <span>Forge Neo URL <span class="muted">(optional; blank disables image generation)</span></span>
          <input
            class="field"
            bind:value={settingsForgeUrl}
            placeholder="https://forge.example.com"
            onkeydown={(e) => e.key === 'Enter' && saveSettings()}
          />
        </label>
        {#if settingsForgeUrl.trim()}
          <div class="imgmodels">
            <div class="imgmodels-head">
              <span class="muted">Models{imageModels.length ? ` · ${imageModels.length}` : ''}</span>
              <button class="refresh" onclick={fetchImageModels} disabled={imageFetching}>
                {imageFetching ? 'Fetching…' : imageModels.length ? 'Refresh from Forge' : 'Fetch from Forge'}
              </button>
            </div>
            {#if imageError}
              <div class="imgmodels-err">Couldn't reach Forge: {imageError}</div>
            {/if}
            {#if imageModels.length}
              <div class="imgmodels-hint muted">
                <b>Auto</b> — may be chosen automatically. <b>Default</b> — used when none is named. Heavy models stay opt-in (they can stall a shared GPU).
              </div>
              <ul class="imgmodels-list">
                {#each imageModels as m (m.name)}
                  <li class="imgmodel" class:dim={!m.autoPick}>
                    <div class="imgmodel-main">
                      <button class="imgmodel-exp" title="Edit recipe (CFG, steps, …)" onclick={() => (m._open = !m._open)}>{m._open ? '▾' : '▸'}</button>
                      <span class="imgmodel-name" title={m.note || ''}>{m.name}</span>
                      <label class="imgmodel-flag" title="May be selected automatically (safe on a shared GPU)">
                        <input
                          type="checkbox"
                          checked={m.autoPick}
                          onchange={(e) => {
                            m.autoPick = e.currentTarget.checked
                            if (!m.autoPick && imageDefault === m.name) imageDefault = ''
                          }}
                        />
                        auto
                      </label>
                      <label class="imgmodel-flag" title={m.autoPick ? 'Make this the default model' : 'Enable auto first'}>
                        <input
                          type="radio"
                          name="imgdefault"
                          checked={imageDefault === m.name}
                          disabled={!m.autoPick}
                          onchange={() => (imageDefault = m.name)}
                        />
                        default
                      </label>
                    </div>
                    {#if m._open}
                      <div class="imgmodel-recipe">
                        <label>Steps<input type="number" min="1" bind:value={m.steps} /></label>
                        <label>CFG<input type="number" min="0" step="0.5" bind:value={m.cfg} /></label>
                        <label>Sampler<input list="imgsamplers" bind:value={m.sampler} /></label>
                        <label>Scheduler<input list="imgschedulers" bind:value={m.scheduler} /></label>
                        <label>Width<input type="number" min="64" step="64" bind:value={m.width} /></label>
                        <label>Height<input type="number" min="64" step="64" bind:value={m.height} /></label>
                        <label class="wide">Modules <span class="muted">(comma-separated — for DiTs like z_image / flux)</span><input bind:value={m._modules} placeholder="e.g. qwen3_4b, z_image_ae" /></label>
                      </div>
                    {/if}
                  </li>
                {/each}
              </ul>
              <datalist id="imgsamplers">
                <option>Euler</option><option>Euler a</option><option>DPM++ 2M</option><option>DPM++ 2M SDE</option><option>DPM++ 3M SDE</option><option>DPM++ SDE</option><option>ER SDE</option><option>LMS</option><option>Heun</option><option>DDIM</option><option>UniPC</option>
              </datalist>
              <datalist id="imgschedulers">
                <option>simple</option><option>karras</option><option>normal</option><option>exponential</option><option>beta</option><option>sgm_uniform</option>
              </datalist>
            {:else if !imageFetching}
              <div class="imgmodels-hint muted">No models yet — Fetch from Forge to load the checkpoints it has.</div>
            {/if}
          </div>
        {/if}

        <div class="group-head">MCP servers <span class="muted">(external tool servers)</span></div>
        <div class="imgmodels">
          <div class="imgmodels-head">
            <span class="muted">Servers{mcpServers.length ? ` · ${mcpServers.length}` : ''}</span>
            <button class="refresh" onclick={addMCPServer}>+ Add server</button>
          </div>
          {#if mcpServers.length}
            <div class="imgmodels-hint muted">Streamable-HTTP servers. Changes connect on the next message — no restart. Enable them per chat in the Tools panel.</div>
            <ul class="imgmodels-list">
              {#each mcpServers as sv (sv._id)}
                <li class="mcpserver">
                  {#if sv.editable}
                    <div class="mcpserver-head">
                      <span class="conn-dot" class:on={sv.connected} title={sv.connected ? 'connected' : 'not connected'}></span>
                      <input class="field mcp-name" placeholder="name" bind:value={sv.name} />
                      <button class="imgmodel-exp" title="Remove server" onclick={() => removeMCPServer(sv)}>✕</button>
                    </div>
                    <input class="field" placeholder="https://server.example/mcp" bind:value={sv.url} />
                    <input class="field" placeholder="Authorization header — optional (e.g. Bearer …)" bind:value={sv.header} />
                  {:else}
                    <div class="mcpserver-head">
                      <span class="conn-dot" class:on={sv.connected} title={sv.connected ? 'connected' : 'not connected'}></span>
                      <span class="mcp-name-ro">{sv.name}</span>
                      <span class="muted">{sv.transport} — edit in mcp.json</span>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {:else}
            <div class="imgmodels-hint muted">No MCP servers. Add one to bring in external tools.</div>
          {/if}
        </div>

        <div class="group-head">Location</div>
        <label class="setting">
          <span>Location <span class="muted">(optional, for place-aware tools — set how broad you like)</span></span>
          <input
            class="field"
            bind:value={settingsLocation}
            placeholder="e.g. Quito, Ecuador — blank to share none"
            onkeydown={(e) => e.key === 'Enter' && saveSettings()}
          />
        </label>

        {#if isDesktop && autostartSupported}
          <div class="group-head">Startup</div>
          <label class="setting">
            <span>Start llaminGO when I log in</span>
            <input type="checkbox" checked={autostartEnabled} onchange={(e) => setAutostart(e.currentTarget.checked, autostartMinimized)} style="width:auto" />
          </label>
          {#if autostartEnabled}
            <label class="setting">
              <span>Start minimized to the tray <span class="muted">(only when launched at login)</span></span>
              <input type="checkbox" checked={autostartMinimized} onchange={(e) => setAutostart(true, e.currentTarget.checked)} style="width:auto" />
            </label>
          {/if}
        {/if}

        <div class="group-head">Display</div>
        <label class="setting">
          <span>Show thinking &amp; tool activity <span class="muted">(off = chat only; view-only, saved per browser)</span></span>
          <input type="checkbox" checked={showDetail} onchange={(e) => setShowDetail(e.currentTarget.checked)} style="width:auto" />
        </label>

        <div class="group-head">Debug</div>
        <label class="setting">
          <span>Debug logging <span class="muted">(save raw llama / Forge / tool payloads to each chat's logs/ folder)</span></span>
          <input type="checkbox" bind:checked={settingsDebug} style="width:auto" />
        </label>
        </div>
        <div class="modal-foot">
          <button class="primary" onclick={saveSettings}>Save &amp; connect</button>
        </div>
      </div>
    </div>
  {/if}
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
    position: relative;
    scrollbar-width: thin;
    scrollbar-color: color-mix(in srgb, var(--ink) 22%, transparent) transparent;
  }
  .topbar,
  .statusbar {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 12px;
    background: var(--surface);
  }
  .topbar {
    height: 44px;
    border-bottom: 0.5px solid var(--border);
    --wails-draggable: drag;
  }
  .topbar button,
  .topbar .mark {
    --wails-draggable: no-drag;
  }
  .winctl {
    display: flex;
    align-self: stretch;
    flex-shrink: 0;
    margin-left: 4px;
    margin-right: -12px;
  }
  .winbtn {
    width: 42px;
    border: none;
    background: none;
    color: var(--muted);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  .winbtn:hover {
    background: var(--paper);
    color: var(--ink);
  }
  .winbtn.close:hover {
    background: #d64545;
    color: #fff;
  }
  .mark-btn {
    border: none;
    background: none;
    padding: 0;
    cursor: pointer;
    display: flex;
    align-items: center;
  }
  .mark-btn:hover {
    opacity: 0.85;
  }
  .icon-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    padding: 0;
    border: none;
    background: none;
    color: var(--muted);
    border-radius: 7px;
    cursor: pointer;
  }
  .icon-btn:hover {
    background: var(--paper);
    color: var(--ink);
  }
  .sysmenu-backdrop {
    position: fixed;
    inset: 0;
    z-index: 20;
  }
  .sysmenu {
    position: absolute;
    top: 46px;
    left: 8px;
    z-index: 21;
    min-width: 184px;
    background: var(--surface);
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 4px;
    display: flex;
    flex-direction: column;
  }
  .sysmenu button {
    text-align: left;
    border: none;
    background: none;
    color: var(--ink);
    font: inherit;
    padding: 7px 10px;
    border-radius: 6px;
    cursor: pointer;
  }
  .sysmenu button:hover {
    background: var(--paper);
  }
  .sysmenu button.danger:hover {
    background: #d64545;
    color: #fff;
  }
  /* The ⋮ actions menu anchors under its button on the right, not the left. */
  .actionsmenu {
    left: auto;
    right: 8px;
  }
  /* In thin mode the ⋮ lives in the bottom bar, so its menu opens upward. */
  .actionsmenu.up {
    top: auto;
    bottom: 40px;
  }
  .statusbar {
    height: 32px;
    border-top: 0.5px solid var(--border);
    font-size: 12px;
    color: var(--muted);
  }
  .mark {
    width: 24px;
    height: 24px;
    border-radius: 6px;
    display: block;
  }
  .wordmark {
    font-weight: 500;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .spacer {
    flex: 1;
  }
  .dot {
    width: 8px;
    height: 8px;
    border-radius: 50%;
    background: #4a9e5c;
  }
  .dot.off {
    background: #e0932a;
  }
  .notice {
    display: block;
    width: 100%;
    text-align: left;
    border: none;
    border-bottom: 0.5px solid var(--border);
    background: color-mix(in srgb, #e0932a 15%, transparent);
    color: var(--ink);
    font: inherit;
    font-size: 13px;
    padding: 8px 12px;
    cursor: pointer;
  }
  .body {
    display: flex;
    flex: 1;
    min-height: 0;
    overflow: hidden;
    position: relative;
  }
  .left {
    width: var(--sidebar-w, 240px);
    flex: none;
    border-right: 0.5px solid var(--border);
    background: var(--surface);
    display: flex;
    flex-direction: column;
    padding: 8px;
    gap: 6px;
    overflow-y: auto;
    transition: margin-left 0.18s ease;
  }
  .left.collapsed {
    border-right-width: 0;
  }
  .resizer {
    position: relative;
    flex: none;
    width: 6px;
    margin: 0 -3px;
    z-index: 2;
    cursor: col-resize;
    background: transparent;
    transition: background 0.15s ease;
  }
  .resizer:hover {
    background: color-mix(in srgb, var(--clay) 35%, transparent);
  }
  /* Thin windows: the sidebar becomes an overlay drawer (hidden by default,
     revealed by the toggle or a swipe) instead of splitting the width. The
     breakpoint lives in JS (NARROW_BP / matchMedia), which sets .narrow here. */
  .body.narrow .left {
    position: absolute;
    top: 0;
    bottom: 0;
    left: 0;
    z-index: 5;
    max-width: 85vw;
    box-shadow: 2px 0 12px color-mix(in srgb, var(--ink) 25%, transparent);
  }
  .body.narrow .resizer {
    display: none;
  }
  .drawer-scrim {
    position: absolute;
    inset: 0;
    z-index: 4;
    background: color-mix(in srgb, var(--ink) 30%, transparent);
  }
  .switcher {
    font-weight: 500;
    padding: 4px 6px;
  }
  .new {
    text-align: left;
    background: none;
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 7px 10px;
    color: var(--clay);
    cursor: pointer;
    font: inherit;
  }
  .new:hover {
    background: var(--paper);
  }
  .profile {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 2px 4px 8px;
  }
  .profile-who {
    font-size: 13px;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .profile-btn {
    margin-left: auto;
    flex-shrink: 0;
    background: none;
    border: 0.5px solid var(--border);
    border-radius: 7px;
    padding: 3px 9px;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .profile-btn:hover {
    background: var(--paper);
    color: var(--ink);
  }
  .profile-btn.signin {
    color: var(--clay);
    border-color: color-mix(in srgb, var(--clay) 40%, transparent);
  }
  .login-err {
    color: #d66;
    font-size: 13px;
  }
  .list {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .sublist {
    list-style: none;
    margin: 2px 0 4px;
    padding: 0;
  }
  .row.sub {
    padding-left: 22px;
    font-size: 13px;
  }
  .row.sub.add {
    color: var(--clay);
  }
  .path {
    padding: 7px 10px;
    font-size: 13px;
  }
  .row {
    display: block;
    width: 100%;
    text-align: left;
    padding: 7px 8px;
    border-radius: 8px;
    border: none;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .row:hover {
    background: var(--paper);
  }
  .row.active {
    background: color-mix(in srgb, var(--clay) 12%, transparent);
    color: var(--clay);
  }
  .row.muted {
    cursor: default;
  }
  .row.muted:hover {
    background: none;
  }
  .muted {
    color: var(--muted);
  }
  .conversation {
    flex: 1;
    display: flex;
    flex-direction: column;
    min-width: 0;
  }
  .thread {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
  }
  /* "Jump to latest" pill — sticky to the bottom of the scrollport, shown only
     when the reader has scrolled up away from a growing conversation. */
  .jumpdown {
    position: sticky;
    bottom: 10px;
    display: block;
    width: fit-content;
    margin: 8px auto 0;
    padding: 5px 13px;
    border: 0.5px solid var(--border);
    border-radius: 999px;
    background: var(--surface);
    color: var(--ink);
    font: inherit;
    font-size: 13px;
    cursor: pointer;
    box-shadow: 0 2px 10px rgba(0, 0, 0, 0.25);
  }
  .jumpdown:hover {
    background: var(--paper);
  }
  .empty {
    text-align: center;
    margin-top: 2rem;
  }
  .msg {
    max-width: 80%;
    padding: 8px 12px;
    border-radius: 12px;
    margin: 0 0 10px;
    border: 0.5px solid var(--border);
    white-space: pre-wrap;
    line-height: 1.5;
  }
  .msg.user {
    margin-left: auto;
    background: color-mix(in srgb, var(--clay) 10%, transparent);
    border-color: color-mix(in srgb, var(--clay) 28%, transparent);
  }
  .error {
    color: #d64545;
  }
  .report {
    color: var(--muted);
  }
  .meta {
    display: flex;
    gap: 8px;
    align-items: baseline;
    font-size: 11px;
    color: var(--muted);
    opacity: 0.7;
    margin: -8px 2px 10px;
  }
  .meta.mine {
    justify-content: flex-end;
  }
  .metabtn {
    border: none;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 11px;
    padding: 0;
    cursor: pointer;
    opacity: 0.65;
  }
  .metabtn:hover {
    opacity: 1;
    color: var(--clay);
  }
  .statusbtn {
    border: 0.5px solid var(--border);
    border-radius: 6px;
    padding: 2px 8px;
    background: none;
    color: var(--clay);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    flex-shrink: 0;
  }
  .statusbtn:hover:not(:disabled) {
    background: var(--paper);
  }
  .statusbtn:disabled {
    color: var(--muted);
    cursor: default;
  }
  /* Thin mode: the status bar is the action bar — icon buttons (Chats, Compact,
     ⋮) and a taller strip for comfortable touch targets; the model text yields. */
  .statusbtn.icon {
    padding: 0;
    width: 34px;
    height: 30px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
  }
  .statusbar.narrow {
    height: 44px;
  }
  .statusbar.narrow .statusmodel {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .thinking {
    max-width: 80%;
    margin: 0 0 10px;
    font-size: 13px;
    color: var(--muted);
  }
  .thinking summary {
    cursor: pointer;
    user-select: none;
    padding: 2px 0;
  }
  .thinking .rtext {
    white-space: pre-wrap;
    line-height: 1.5;
    margin-top: 4px;
    padding: 6px 10px;
    border-left: 2px solid var(--border);
  }
  .composer {
    border-top: 0.5px solid var(--border);
    padding: 12px;
  }
  .composer-controls {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
  }
  .field {
    width: 100%;
    padding: 10px 12px;
    border: 0.5px solid var(--border);
    border-radius: 8px;
    background: var(--paper);
    color: var(--ink);
    font: inherit;
  }
  /* The composer grows with its content (Shift+Enter adds a line; Enter sends),
     up to a cap, then scrolls. Height is driven in JS; these are the bounds. */
  .composer-input {
    box-sizing: border-box;
    display: block;
    resize: none;
    overflow-y: auto;
    min-height: 40px;
    max-height: 160px;
    line-height: 1.4;
  }
  .rename {
    padding: 6px 8px;
    box-sizing: border-box;
  }
  .rename.sub {
    margin-left: 14px;
    width: calc(100% - 14px);
    font-size: 13px;
  }
  .rowwrap {
    display: flex;
    align-items: center;
    gap: 2px;
  }
  .rowwrap .row {
    flex: 1;
    min-width: 0;
  }
  .del {
    flex: none;
    border: none;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    padding: 4px 8px;
    border-radius: 6px;
    cursor: pointer;
    opacity: 0;
  }
  .rowwrap:hover .del {
    opacity: 0.55;
  }
  .del:hover {
    opacity: 1;
    color: #d64545;
    background: var(--paper);
  }
  .confirmdel {
    flex: 1;
    min-width: 0;
    font-size: 13px;
    color: var(--muted);
    padding: 0 6px;
  }
  .del-yes,
  .del-no {
    flex: none;
    border: 0.5px solid var(--border);
    border-radius: 6px;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    padding: 3px 8px;
    cursor: pointer;
  }
  .del-yes {
    color: #d64545;
    border-color: color-mix(in srgb, #d64545 40%, transparent);
  }
  .del-yes:hover,
  .del-no:hover {
    background: var(--paper);
  }
  .toolcall {
    max-width: 80%;
    margin: 0 0 8px;
    font-size: 12px;
    color: var(--muted);
  }
  .toolcall strong {
    font-weight: 600;
    color: var(--clay);
  }
  .consent {
    margin: 0 12px 12px;
    border: 0.5px solid color-mix(in srgb, var(--clay) 40%, transparent);
    background: color-mix(in srgb, var(--clay) 8%, transparent);
    border-radius: 12px;
    padding: 12px;
  }
  .consent-head {
    display: flex;
    align-items: center;
    gap: 8px;
    font-weight: 500;
    margin-bottom: 6px;
  }
  .risk {
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    border-radius: 6px;
    padding: 1px 7px;
    color: var(--muted);
    border: 0.5px solid var(--border);
  }
  .risk.medium {
    color: #e0932a;
    border-color: color-mix(in srgb, #e0932a 40%, transparent);
  }
  .risk.high {
    color: #d64545;
    border-color: color-mix(in srgb, #d64545 40%, transparent);
  }
  .consent-label {
    margin-bottom: 6px;
  }
  .consent-detail {
    max-height: 160px;
    overflow: auto;
    margin: 0 0 10px;
    padding: 8px 10px;
    border-radius: 8px;
    background: var(--paper);
    border: 0.5px solid var(--border);
    font-family: ui-monospace, "Cascadia Code", Consolas, monospace;
    font-size: 12px;
    white-space: pre-wrap;
    line-height: 1.45;
  }
  .consent-actions {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }
  .consent-actions button {
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 7px 14px;
    background: none;
    color: var(--ink);
    font: inherit;
    cursor: pointer;
  }
  .consent-actions button:hover {
    background: var(--paper);
  }
  .consent-actions .primary {
    color: var(--clay);
    border-color: color-mix(in srgb, var(--clay) 40%, transparent);
  }
  .bar {
    display: inline-block;
    width: 120px;
    height: 7px;
    border-radius: 999px;
    overflow: hidden;
    background: color-mix(in srgb, var(--ink) 10%, transparent);
  }
  .bar .fill {
    display: block;
    height: 100%;
    background: var(--muted);
  }
  .bar.near .fill {
    background: #e0932a;
  }
  .bar.full .fill {
    background: #d64545;
  }
  /* Context pill + popover: the budget as one status-bar control. The pill is a
     battery filled to the usage %, colored by level; pressing it opens a popover
     with the full bar, the exact count, and Compact. */
  .ctxwrap {
    position: relative;
    display: inline-flex;
    flex-shrink: 0;
  }
  .ctx-pill {
    display: inline-flex;
    align-items: center;
    gap: 5px;
  }
  .ctx-pill .ctx-pct {
    font-variant-numeric: tabular-nums;
  }
  .ctx-pill.near {
    color: #e0932a;
    border-color: color-mix(in srgb, #e0932a 40%, transparent);
  }
  .ctx-pill.full {
    color: #d64545;
    border-color: color-mix(in srgb, #d64545 40%, transparent);
  }
  .ctx-backdrop {
    position: fixed;
    inset: 0;
    z-index: 20;
  }
  .ctx-pop {
    position: absolute;
    bottom: calc(100% + 6px);
    right: 0;
    z-index: 21;
    min-width: 180px;
    background: var(--surface);
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 10px;
    display: flex;
    flex-direction: column;
    gap: 8px;
  }
  .ctx-pop .bar {
    width: 100%;
    display: block;
    height: 8px;
  }
  .ctx-count {
    font-size: 12px;
    color: var(--muted);
    font-variant-numeric: tabular-nums;
    text-align: center;
  }
  .ctx-pop .statusbtn {
    align-self: center;
  }
  .ctx-bang {
    color: #d64545;
    font-weight: 700;
  }
  .ctx-warn {
    font-size: 11px;
    line-height: 1.35;
    color: #d64545;
    background: color-mix(in srgb, #d64545 8%, transparent);
    border: 0.5px solid color-mix(in srgb, #d64545 30%, transparent);
    border-radius: 6px;
    padding: 6px 8px;
  }
  .ctx-warn.soft {
    color: #e0932a;
    background: color-mix(in srgb, #e0932a 8%, transparent);
    border-color: color-mix(in srgb, #e0932a 30%, transparent);
  }
  /* Connection picker: the active llama-server (= model) on the status-bar left;
     click to switch among saved connections. Opens upward like the ctx popover. */
  .connwrap {
    position: relative;
    display: inline-flex;
    min-width: 0;
  }
  .statusmodel.connpick {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    border: none;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    padding: 2px 4px;
    border-radius: 6px;
    min-width: 0;
  }
  .statusmodel.connpick:hover {
    background: var(--paper);
  }
  .connpick-label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .connpick .caret {
    opacity: 0.6;
    flex-shrink: 0;
  }
  .conn-backdrop {
    position: fixed;
    inset: 0;
    z-index: 20;
  }
  .conn-pop {
    position: absolute;
    bottom: calc(100% + 6px);
    left: 0;
    z-index: 21;
    min-width: 240px;
    max-width: 360px;
    background: var(--surface);
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 4px;
    display: flex;
    flex-direction: column;
  }
  .conn-row {
    display: flex;
    align-items: center;
    gap: 8px;
    text-align: left;
    border: none;
    background: none;
    color: var(--ink);
    font: inherit;
    font-size: 12px;
    padding: 7px 9px;
    border-radius: 6px;
    cursor: pointer;
    min-width: 0;
  }
  .conn-row:hover {
    background: var(--paper);
  }
  .conn-row.active .conn-row-name {
    color: var(--clay);
  }
  .conn-row-name {
    font-weight: 500;
    flex-shrink: 0;
  }
  .conn-row-url {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .conn-manage {
    text-align: left;
    border: none;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    padding: 7px 9px;
    border-radius: 6px;
    cursor: pointer;
    border-top: 0.5px solid var(--border);
    margin-top: 2px;
  }
  .conn-manage:hover {
    background: var(--paper);
  }
  .conn-temp {
    flex-shrink: 0;
    font-variant-numeric: tabular-nums;
  }
  .conn-sampling {
    display: flex;
    gap: 12px;
    margin-top: 2px;
  }
  .conn-sampling label {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: var(--muted);
  }
  .conn-num {
    width: 84px;
    padding: 6px 8px;
  }
  .conn-prompt {
    resize: vertical;
    min-height: 42px;
    line-height: 1.4;
    font-family: inherit;
  }
  .conn-radio {
    display: inline-flex;
    align-items: center;
  }
  .summary-mark summary {
    color: var(--clay);
  }
  .compacting {
    text-align: center;
    font-size: 12px;
    padding: 6px 0;
  }
  .pill-btn {
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 3px 10px;
    background: none;
    color: var(--clay);
    font: inherit;
    cursor: pointer;
  }
  .pill-btn:hover {
    background: var(--paper);
  }
  .pill-btn.on {
    color: var(--clay);
    border-color: color-mix(in srgb, var(--clay) 40%, transparent);
    background: color-mix(in srgb, var(--clay) 12%, transparent);
  }
  /* The composer send button: a filled accent so it reads as the primary action
     (needed on mobile, where there's no Enter-to-send affordance). */
  .send-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 30px;
    height: 30px;
    padding: 0;
    border-color: transparent;
    background: var(--clay);
    color: #fff;
  }
  .send-btn:hover {
    background: color-mix(in srgb, var(--clay) 85%, black);
  }
  .send-btn:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .setting {
    display: flex;
    flex-direction: column;
    gap: 4px;
    font-size: 13px;
  }
  .setting > span {
    color: var(--muted);
  }
  .group-head {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--muted);
    margin-top: 4px;
  }
  .imgmodels {
    padding: 10px 12px;
    border: 0.5px solid var(--border);
    border-radius: 8px;
    background: color-mix(in srgb, var(--ink) 3%, transparent);
  }
  .imgmodels-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }
  .imgmodels-head .muted {
    font-size: 12px;
  }
  .imgmodels-head button.refresh {
    font: inherit;
    font-size: 12px;
    padding: 3px 10px;
    border: 0.5px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--ink);
    cursor: pointer;
  }
  .imgmodels-head button.refresh:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .imgmodels-hint {
    font-size: 12px;
    line-height: 1.35;
    margin: 8px 0 0;
  }
  .imgmodels-err {
    font-size: 12px;
    color: var(--danger, #c0392b);
    margin: 8px 0 0;
  }
  .imgmodels-list {
    list-style: none;
    margin: 6px 0 0;
    padding: 0;
  }
  .imgmodel {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 6px 0;
    border-top: 0.5px solid var(--border);
  }
  .imgmodel-main {
    display: flex;
    align-items: center;
    gap: 10px;
  }
  .imgmodel.dim .imgmodel-name {
    opacity: 0.55;
  }
  .imgmodel-name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    font-size: 13px;
  }
  .imgmodel-flag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: var(--muted);
    flex-shrink: 0;
    cursor: pointer;
  }
  .imgmodel-flag input {
    width: auto;
    margin: 0;
    cursor: pointer;
  }
  .imgmodel-exp {
    background: none;
    border: none;
    color: var(--muted);
    cursor: pointer;
    font: inherit;
    font-size: 11px;
    padding: 0 2px;
    flex-shrink: 0;
  }
  .imgmodel-recipe {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 6px 10px;
    margin: 2px 0 4px 22px;
  }
  .imgmodel-recipe label {
    display: flex;
    flex-direction: column;
    gap: 2px;
    font-size: 11px;
    color: var(--muted);
  }
  .imgmodel-recipe label.wide {
    grid-column: 1 / -1;
  }
  .imgmodel-recipe input {
    background: var(--paper);
    color: var(--ink);
    border: 0.5px solid var(--border);
    border-radius: 6px;
    padding: 4px 6px;
    font: inherit;
    font-size: 12px;
    width: 100%;
  }
  .toollist {
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 6px 10px;
  }
  .toolrow {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 3px 0;
    cursor: pointer;
  }
  .toolrow input {
    width: auto;
    margin: 0;
    cursor: pointer;
  }
  .toolname {
    font-size: 13px;
  }
  .toolgroup {
    border-top: 0.5px solid var(--border);
    padding: 7px 0;
  }
  .toolgroup:first-of-type {
    border-top: none;
  }
  .toolgroup-head {
    display: flex;
    align-items: center;
    gap: 6px;
  }
  .toolgroup-name {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 500;
    cursor: pointer;
  }
  .toolgroup-name input {
    width: auto;
    margin: 0;
    cursor: pointer;
  }
  .toolgroup-name .muted {
    font-weight: 400;
    font-size: 12px;
  }
  .toolgroup-list {
    list-style: none;
    margin: 4px 0 2px 22px;
    padding: 0;
  }
  .mcpserver {
    display: flex;
    flex-direction: column;
    gap: 5px;
    padding: 8px 0;
    border-top: 0.5px solid var(--border);
  }
  .mcpserver:first-child {
    border-top: none;
  }
  .mcpserver-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .mcpserver-head .muted {
    font-size: 12px;
  }
  .mcp-name {
    flex: 1;
  }
  .mcp-name-ro {
    flex: 1;
    font-size: 13px;
  }
  .conn-dot {
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: color-mix(in srgb, var(--ink) 25%, transparent);
    flex-shrink: 0;
  }
  .conn-dot.on {
    background: var(--ok);
  }
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--ink) 40%, transparent);
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 24px;
    z-index: 10;
  }
  .modal {
    width: min(560px, 100%);
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border: 0.5px solid var(--border);
    border-radius: 12px;
    padding: 16px;
    gap: 10px;
  }
  .modal-head {
    display: flex;
    align-items: center;
    font-weight: 500;
    flex-shrink: 0;
  }
  .modal-head .x {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--muted);
    cursor: pointer;
    font: inherit;
  }
  /* The settings content scrolls as one; the header and footer stay put. */
  .modal-body {
    flex: 1 1 auto;
    min-height: 0;
    overflow-y: auto;
    padding-right: 10px;
    scrollbar-gutter: stable;
    scrollbar-width: thin;
    scrollbar-color: color-mix(in srgb, var(--ink) 28%, transparent) transparent;
  }
  .modal-body > * + * {
    margin-top: 10px;
  }
  .modal-body::-webkit-scrollbar {
    width: 10px;
  }
  .modal-body::-webkit-scrollbar-track {
    background: transparent;
  }
  .modal-body::-webkit-scrollbar-thumb {
    background: color-mix(in srgb, var(--ink) 22%, transparent);
    background-clip: padding-box;
    border: 2px solid transparent;
    border-radius: 6px;
  }
  .modal-body::-webkit-scrollbar-thumb:hover {
    background: color-mix(in srgb, var(--ink) 36%, transparent);
    background-clip: padding-box;
    border: 2px solid transparent;
    border-radius: 6px;
  }
  .warn {
    background: color-mix(in srgb, #e0932a 15%, transparent);
    border: 0.5px solid color-mix(in srgb, #e0932a 40%, transparent);
    border-radius: 8px;
    padding: 8px 10px;
    font-size: 13px;
  }
  .memo {
    flex: 1;
    min-height: 240px;
    resize: vertical;
    padding: 10px 12px;
    border: 0.5px solid var(--border);
    border-radius: 8px;
    background: var(--paper);
    color: var(--ink);
    font: inherit;
    line-height: 1.5;
  }
  .modal-foot {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    flex-shrink: 0;
  }
  .modal-foot button {
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 7px 14px;
    background: none;
    color: var(--ink);
    font: inherit;
    cursor: pointer;
  }
  .modal-foot .primary {
    color: var(--clay);
    border-color: color-mix(in srgb, var(--clay) 40%, transparent);
  }
  .modal-foot button:hover {
    background: var(--paper);
  }
  .prose {
    white-space: normal;
  }
  .prose :global(img) {
    max-width: 100%;
    height: auto;
    border-radius: 8px;
    margin: 4px 0;
  }
  .prose :global(p) {
    margin: 0 0 8px;
  }
  .prose :global(p:last-child) {
    margin-bottom: 0;
  }
  .prose :global(ul),
  .prose :global(ol) {
    margin: 0 0 8px;
    padding-left: 20px;
  }
  .prose :global(li) {
    margin: 2px 0;
  }
  .prose :global(h1),
  .prose :global(h2),
  .prose :global(h3),
  .prose :global(h4) {
    margin: 12px 0 6px;
    font-weight: 600;
    line-height: 1.3;
  }
  .prose :global(h1) {
    font-size: 1.3em;
  }
  .prose :global(h2) {
    font-size: 1.15em;
  }
  .prose :global(h3) {
    font-size: 1.05em;
  }
  .prose :global(code) {
    font-family: ui-monospace, "Cascadia Code", Consolas, monospace;
    font-size: 0.9em;
    background: color-mix(in srgb, var(--ink) 8%, transparent);
    padding: 1px 5px;
    border-radius: 4px;
  }
  .prose :global(pre) {
    position: relative;
    background: color-mix(in srgb, var(--ink) 8%, transparent);
    padding: 10px 12px;
    border-radius: 8px;
    overflow-x: auto;
    margin: 0 0 8px;
  }
  :global(.codecopy) {
    position: absolute;
    top: 6px;
    right: 6px;
    border: 0.5px solid var(--border);
    border-radius: 6px;
    background: var(--surface);
    color: var(--muted);
    font: inherit;
    font-size: 11px;
    padding: 2px 7px;
    cursor: pointer;
    opacity: 0.75;
  }
  :global(.codecopy):hover {
    opacity: 1;
    color: var(--clay);
  }
  .prose :global(pre code) {
    background: none;
    padding: 0;
    font-size: 0.85em;
  }
  .prose :global(blockquote) {
    margin: 0 0 8px;
    padding-left: 10px;
    border-left: 2px solid var(--border);
    color: var(--muted);
  }
  .prose :global(a) {
    color: var(--clay);
  }
  .prose :global(table) {
    border-collapse: collapse;
    margin: 0 0 8px;
  }
  .prose :global(th),
  .prose :global(td) {
    border: 0.5px solid var(--border);
    padding: 4px 8px;
  }
  .prose :global(hr) {
    border: none;
    border-top: 0.5px solid var(--border);
    margin: 12px 0;
  }
</style>

<script>
  import { onMount } from 'svelte'
  import MarkdownIt from 'markdown-it'

  // Render assistant markdown safely: html:false escapes any raw HTML the model
  // emits, so {@html} only ever sees markdown-generated tags.
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true })

  // The desktop app (Wails) cannot stream through its asset server, so it serves
  // the core API on a loopback port the webview calls directly; the browser and
  // PWA use same-origin relative paths.
  const apiFetch = (path, opts) => fetch((window.runtime ? 'http://127.0.0.1:8766' : '') + path, opts)

  let conn = $state(null) // { model, nCtx }
  let chats = $state([])
  let activeId = $state(null)
  let messages = $state([]) // { role, content }
  let input = $state('')
  let streaming = $state(false)
  let budget = $state(null) // { used, nCtx, level }
  let error = $state(null)
  let activeSummary = $state(null) // the active compaction summary, if any
  let report = $state(null) // transient compaction report

  let projects = $state([])
  let expandedProject = $state(null)
  let sessions = $state([]) // sessions of the expanded project
  let openingFolder = $state(false)
  let folderPath = $state('')

  let memoryOpen = $state(false)
  let memoryContent = $state('')
  let memoryTampered = $state(false)

  let reachable = $state(true)
  let staleNote = $state(null) // transient "server changed" note

  let pending = $state(null) // a tool's consent prompt: { id, label, detail, risk, scopes }

  onMount(() => {
    refresh().catch((e) => (error = String(e)))
    const t = setInterval(checkServer, 15000)
    return () => clearInterval(t)
  })

  // Monitor the server: warn if the model or context size changed under us, and
  // reflect when it becomes unreachable.
  async function checkServer() {
    try {
      const p = await apiFetch('/api/props').then((r) => {
        if (!r.ok) throw new Error('unreachable')
        return r.json()
      })
      reachable = true
      if (conn && (p.model !== conn.model || p.nCtx !== conn.nCtx)) {
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
    conn = await apiFetch('/api/props').then((r) => r.json())
    chats = (await apiFetch('/api/chats').then((r) => r.json())) ?? []
    projects = (await apiFetch('/api/projects').then((r) => r.json())) ?? []
    if (!activeId && chats.length) await selectChat(chats[0].id)
  }

  // Open a conversation: the core owns history, so load its log from disk.
  async function selectChat(id) {
    activeId = id
    error = null
    budget = null
    report = null
    pending = null
    const entries = (await apiFetch(`/api/conversations/${id}`).then((r) => r.json())) ?? []
    const summaries = entries.filter((e) => e.kind === 'summary')
    activeSummary = summaries.length ? summaries[summaries.length - 1] : null
    messages = entries.filter((e) => e.kind === 'msg').map((e) => ({ role: e.role, content: e.content }))
  }

  // Compaction folds the oldest messages into a summary (non-destructive); the
  // core returns how many it folded and the tokens saved.
  async function compact() {
    if (!activeId || streaming) return
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
    input = ''
    messages.push({ role: 'user', content: text })
    messages.push({ role: 'assistant', content: '', reasoning: '', tools: [] })
    streaming = true
    try {
      // The client sends only the new turn; the core persists both sides.
      await streamChat(activeId, text)
    } catch (e) {
      error = String(e)
      reachable = false
    } finally {
      streaming = false
    }
  }

  async function streamChat(id, content) {
    const resp = await apiFetch('/api/chat/stream', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id, content }),
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
    if (event === 'delta' && payload.content) {
      messages[messages.length - 1].content += payload.content
    } else if (event === 'reasoning' && payload.content) {
      messages[messages.length - 1].reasoning += payload.content
    } else if (event === 'compacted') {
      report = `Auto-compacted: folded ${payload.folded}, saved ~${payload.saved} tokens`
      if (payload.summary) activeSummary = { content: payload.summary }
    } else if (event === 'tool_call') {
      const last = messages[messages.length - 1]
      if (last) (last.tools ??= []).push({ name: payload.name, arguments: payload.arguments })
    } else if (event === 'permission') {
      pending = payload
    } else if (event === 'done') {
      budget = payload.budget
      reachable = true
    } else if (event === 'error') {
      error = payload.error
    }
  }

  function modelName(path) {
    if (!path) return 'unknown model'
    return path.split(/[\\/]/).pop().replace(/\.gguf$/, '')
  }

  const pct = $derived(budget && budget.nCtx ? Math.min(100, Math.round((100 * budget.used) / budget.nCtx)) : 0)
</script>

<div class="app">
  <header class="topbar">
    <span class="mark"></span>
    <span class="wordmark">llaminGO</span>
    <span class="spacer"></span>
    <button class="pill-btn" onclick={openMemory}>Memory</button>
    {#if conn}<span class="pill">{modelName(conn.model)}</span>{/if}
  </header>

  {#if staleNote}
    <button class="notice" onclick={() => (staleNote = null)}>{staleNote} · dismiss</button>
  {/if}

  <div class="body">
    <aside class="left">
      <div class="switcher">Chats</div>
      <button class="new" onclick={newChat}>+ New chat</button>
      <ul class="list">
        {#each chats as c}
          <li>
            <button class="row" class:active={c.id === activeId} onclick={() => selectChat(c.id)}>
              {c.title || 'Untitled'}
            </button>
          </li>
        {:else}
          <li class="row muted">No chats yet</li>
        {/each}
      </ul>

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
                  <li>
                    <button class="row sub" class:active={s.id === activeId} onclick={() => selectChat(s.id)}>
                      {s.title || 'Untitled'}
                    </button>
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
    </aside>

    <main class="conversation">
      <div class="thread">
        {#if error}<p class="error">{error}</p>{/if}
        {#if activeSummary}
          <details class="thinking">
            <summary>Earlier conversation summarized</summary>
            <div class="rtext prose">{@html md.render(activeSummary.content)}</div>
          </details>
        {/if}
        {#each messages as m, i}
          {#if m.reasoning}
            <details class="thinking" open={streaming && i === messages.length - 1 && !m.content}>
              <summary>Thinking</summary>
              <div class="rtext prose">{@html md.render(m.reasoning)}</div>
            </details>
          {/if}
          {#if m.role === 'user'}
            <div class="msg user">{m.content}</div>
          {:else}
            {#if m.tools?.length}
              {#each m.tools as t}
                <div class="toolcall" title={t.arguments}>used <strong>{t.name}</strong></div>
              {/each}
            {/if}
            {#if m.content !== ''}
              <div class="msg assistant prose">{@html md.render(m.content)}</div>
            {:else if streaming && i === messages.length - 1 && !m.reasoning}
              <div class="msg assistant">…</div>
            {/if}
          {/if}
        {:else}
          <p class="empty muted">Ask something to get started.</p>
        {/each}
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
        <input
          class="field"
          placeholder="Ask something…"
          bind:value={input}
          onkeydown={(e) => e.key === 'Enter' && send()}
          disabled={streaming}
        />
      </div>
    </main>
  </div>

  <footer class="statusbar">
    {#if conn && reachable}
      <span class="dot"></span>
      <span>{modelName(conn.model)} · {conn.nCtx} ctx</span>
    {:else if conn && !reachable}
      <span class="dot off"></span>
      <span>server unreachable — reconnecting…</span>
    {:else if error}
      <span>couldn't reach the server</span>
    {:else}
      <span>connecting…</span>
    {/if}
    <span class="spacer"></span>
    {#if report}<span class="report">{report}</span>{/if}
    {#if budget}
      <span>context {budget.used} / {budget.nCtx}</span>
      <span class="bar {budget.level}"><span class="fill" style="width:{pct}%"></span></span>
    {/if}
    {#if activeId}<button class="statusbtn" onclick={compact} disabled={streaming}>Compact</button>{/if}
  </footer>

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
</div>

<style>
  .app {
    display: flex;
    flex-direction: column;
    height: 100vh;
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
  }
  .statusbar {
    height: 32px;
    border-top: 0.5px solid var(--border);
    font-size: 12px;
    color: var(--muted);
  }
  .mark {
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--clay);
  }
  .wordmark {
    font-weight: 500;
  }
  .spacer {
    flex: 1;
  }
  .pill {
    border: 0.5px solid var(--border);
    border-radius: 8px;
    padding: 3px 10px;
    color: var(--muted);
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
  }
  .left {
    width: 240px;
    border-right: 0.5px solid var(--border);
    background: var(--surface);
    display: flex;
    flex-direction: column;
    padding: 8px;
    gap: 6px;
    overflow-y: auto;
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
  .statusbtn {
    border: 0.5px solid var(--border);
    border-radius: 6px;
    padding: 2px 8px;
    background: none;
    color: var(--clay);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
  }
  .statusbtn:hover:not(:disabled) {
    background: var(--paper);
  }
  .statusbtn:disabled {
    color: var(--muted);
    cursor: default;
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
  .field {
    width: 100%;
    padding: 10px 12px;
    border: 0.5px solid var(--border);
    border-radius: 8px;
    background: var(--paper);
    color: var(--ink);
    font: inherit;
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
  }
  .modal-head .x {
    margin-left: auto;
    background: none;
    border: none;
    color: var(--muted);
    cursor: pointer;
    font: inherit;
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
    background: color-mix(in srgb, var(--ink) 8%, transparent);
    padding: 10px 12px;
    border-radius: 8px;
    overflow-x: auto;
    margin: 0 0 8px;
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

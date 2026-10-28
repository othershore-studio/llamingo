<script>
  import { onMount } from 'svelte'

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

  onMount(() => {
    refresh().catch((e) => (error = String(e)))
  })

  async function refresh() {
    conn = await fetch('/api/props').then((r) => r.json())
    chats = (await fetch('/api/chats').then((r) => r.json())) ?? []
    projects = (await fetch('/api/projects').then((r) => r.json())) ?? []
    if (!activeId && chats.length) await selectChat(chats[0].id)
  }

  // Open a conversation: the core owns history, so load its log from disk.
  async function selectChat(id) {
    activeId = id
    error = null
    budget = null
    report = null
    const entries = (await fetch(`/api/conversations/${id}`).then((r) => r.json())) ?? []
    const summaries = entries.filter((e) => e.kind === 'summary')
    activeSummary = summaries.length ? summaries[summaries.length - 1] : null
    messages = entries.filter((e) => e.kind === 'msg').map((e) => ({ role: e.role, content: e.content }))
  }

  // Compaction folds the oldest messages into a summary (non-destructive); the
  // core returns how many it folded and the tokens saved.
  async function compact() {
    if (!activeId || streaming) return
    const r = await fetch('/api/compact', {
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

  async function newChat() {
    const created = await fetch('/api/chats', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ title: 'New chat' }),
    }).then((r) => r.json())
    chats = (await fetch('/api/chats').then((r) => r.json())) ?? []
    await selectChat(created.id)
  }

  async function loadSessions(projectId) {
    sessions = (await fetch(`/api/projects/${projectId}/sessions`).then((r) => r.json())) ?? []
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
    const created = await fetch('/api/projects', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path, title: baseName(path) }),
    }).then((r) => r.json())
    folderPath = ''
    openingFolder = false
    projects = (await fetch('/api/projects').then((r) => r.json())) ?? []
    expandedProject = created.id
    await loadSessions(created.id)
  }

  async function newSession(projectId) {
    const created = await fetch(`/api/projects/${projectId}/sessions`, {
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
    messages.push({ role: 'assistant', content: '', reasoning: '' })
    streaming = true
    try {
      // The client sends only the new turn; the core persists both sides.
      await streamChat(activeId, text)
    } catch (e) {
      error = String(e)
    } finally {
      streaming = false
    }
  }

  async function streamChat(id, content) {
    const resp = await fetch('/api/chat/stream', {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ id, content }),
    })
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
    } else if (event === 'done') {
      budget = payload.budget
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
    {#if conn}<span class="pill">{modelName(conn.model)}</span>{/if}
  </header>

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
            <div class="rtext">{activeSummary.content}</div>
          </details>
        {/if}
        {#each messages as m, i}
          {#if m.reasoning}
            <details class="thinking" open={streaming && i === messages.length - 1 && !m.content}>
              <summary>Thinking</summary>
              <div class="rtext">{m.reasoning}</div>
            </details>
          {/if}
          {#if m.role === 'user' || m.content !== ''}
            <div class="msg {m.role}">{m.content}</div>
          {:else if streaming && i === messages.length - 1 && !m.reasoning}
            <div class="msg assistant">…</div>
          {/if}
        {:else}
          <p class="empty muted">Ask something to get started.</p>
        {/each}
      </div>
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
    {#if conn}
      <span class="dot"></span>
      <span>{modelName(conn.model)} · {conn.nCtx} ctx</span>
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
</style>

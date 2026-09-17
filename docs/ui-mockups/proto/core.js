/* coSlash prototype — shared behaviour: inspector drawer, ⌘K palette, toasts.
   Prototype only: Resume and Copy handoff are simulated. */

/* ---------- toast ---------- */
function toast(message) {
  let el = document.querySelector('.toast');
  if (!el) {
    el = document.createElement('div');
    el.className = 'toast';
    document.body.appendChild(el);
  }
  el.textContent = message;
  el.classList.add('show');
  clearTimeout(el._t);
  el._t = setTimeout(() => el.classList.remove('show'), 2200);
}

/* ---------- inspector drawer ---------- */
function mountDrawer() {
  const scrim = document.createElement('div');
  scrim.className = 'scrim';
  const drawer = document.createElement('div');
  drawer.className = 'drawer';
  drawer.setAttribute('role', 'dialog');
  drawer.setAttribute('aria-label', 'Session inspector');
  document.body.append(scrim, drawer);
  scrim.addEventListener('click', closeDrawer);
  return { scrim, drawer };
}

let _drawer = null;
function closeDrawer() {
  if (!_drawer) return;
  _drawer.drawer.classList.remove('open');
  _drawer.scrim.classList.remove('open');
}

function stateMarkup(s) {
  const st = statusOf(s);
  const cls = { active: 'state-active', waiting: 'state-waiting', idle: 'state-idle', off: 'state-off', unknown: 'state-unknown' }[st.tone];
  const pip = { active: 'pip-active', waiting: 'pip-waiting', idle: 'pip-idle', off: 'pip-off', unknown: 'pip-unknown' }[st.tone];
  return `<span class="state ${cls}"><span class="pip ${pip}"></span>${st.label}</span>`;
}

function agentMarkup(s) {
  const a = agentOf(s);
  return `<span class="agent-tag agent-${a.key}"><span class="agent-swatch"></span>${a.label}</span>`;
}

function openSession(id) {
  const s = SESSIONS.find((x) => x.id === id);
  if (!s) return;
  if (!_drawer) _drawer = mountDrawer();
  const { scrim, drawer } = _drawer;

  const canResume = s.status !== 'unknown' && !(s.status === 'busy' && s.agent === 'codex');
  const resumeHint = s.status === 'unknown'
    ? 'Host unreachable — resume unavailable'
    : (s.status === 'busy' && s.agent === 'codex')
      ? 'This session is already active'
      : `Reopens in ${agentOf(s).label} · ${s.source === 'local' ? 'This Mac' : s.source}`;

  drawer.innerHTML = `
    <div class="drawer-head">
      <div class="drawer-title-row">
        ${agentMarkup(s)}
        <span class="drawer-title">${escapeHtml(s.title)}</span>
        <button class="drawer-close" aria-label="Close">✕</button>
      </div>
      <div class="drawer-sub">
        ${stateMarkup(s)}
        <span>${escapeHtml(s.repo)}</span>
        ${s.branch ? `<span class="chip-mono">${escapeHtml(s.branch)}</span>` : ''}
        ${s.source !== 'local' ? `<span class="chip-remote">${escapeHtml(s.source)}</span>` : ''}
        <span>${escapeHtml(s.when)}</span>
        ${s.illustrative ? '<span class="tag-illustrative">illustrative</span>' : ''}
      </div>
    </div>
    <div class="drawer-body">
      <div class="stat-grid">
        <div class="stat-cell"><div class="stat-k">Est. cost</div><div class="stat-v">${money(s.cost)}</div></div>
        <div class="stat-cell"><div class="stat-k">Tokens</div><div class="stat-v">${s.tokens}</div></div>
        <div class="stat-cell"><div class="stat-k">Duration</div><div class="stat-v">${s.duration ?? '—'}</div></div>
        <div class="stat-cell"><div class="stat-k">Turns</div><div class="stat-v">${s.turns}</div></div>
        <div class="stat-cell"><div class="stat-k">Context</div><div class="stat-v">${s.ctx}%</div></div>
        <div class="stat-cell"><div class="stat-k">Files</div><div class="stat-v">${s.files}</div></div>
      </div>

      ${s.question ? `
        <div class="sec-k">Waiting on you</div>
        <div class="ask-box">
          <div class="ask-k">Question</div>
          <div class="ask-q">“${escapeHtml(s.question)}”</div>
        </div>` : ''}

      ${s.status === 'unknown' ? `
        <div class="sec-k">Liveness</div>
        <div class="empty-note">
          <span>Process state could not be read from <b>${escapeHtml(s.source)}</b>. This session is
          <b>not finished</b> — it may still be running.</span>
        </div>` : ''}

      <div class="sec-k">Goal</div>
      <div class="prose prose-quote">${escapeHtml(s.goal)}</div>

      <div class="sec-k">Outcome</div>
      <div class="prose">${escapeHtml(s.outcome)}</div>

      ${s.decisions && s.decisions.length ? `
        <div class="sec-k">Key decisions</div>
        <ul class="decisions">${s.decisions.map((d) => `<li>${escapeHtml(d)}</li>`).join('')}</ul>` : ''}

      ${s.artifacts && s.artifacts.length ? `
        <div class="sec-k">Files touched</div>
        ${s.artifacts.map((a) => `<div class="file-row">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="#9aa0a8" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M14 3v5h5"/><path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/></svg>
          ${escapeHtml(a)}</div>`).join('')}` : ''}

      ${s.subagents ? `
        <div class="sec-k">Subagents</div>
        <div class="empty-note"><span><b>${s.subagents} subagents</b> ran in their own context windows and returned one result each.</span></div>` : ''}

      <div class="sec-k">Session</div>
      <div class="file-row" style="color:#8e8e8e">${escapeHtml(s.id)}</div>
    </div>
    <div class="drawer-foot">
      <button class="btn">Copy handoff</button>
      <button class="btn btn-primary" ${canResume ? '' : 'disabled'}>Resume</button>
      <span class="hint">${escapeHtml(resumeHint)}</span>
    </div>`;

  drawer.querySelector('.drawer-close').addEventListener('click', closeDrawer);
  drawer.querySelector('.btn').addEventListener('click', () => toast('Handoff brief copied (simulated)'));
  const resume = drawer.querySelector('.btn-primary');
  if (canResume) resume.addEventListener('click', () => toast(`Resuming in ${agentOf(s).label} (simulated)`));

  drawer.classList.add('open');
  scrim.classList.add('open');
}

/* ---------- command palette ---------- */
let _pal = null;
let _palIndex = 0;
let _palRows = [];

function mountPalette() {
  const scrim = document.createElement('div');
  scrim.className = 'pal-scrim';
  scrim.innerHTML = `
    <div class="pal" role="dialog" aria-label="Find a session">
      <div class="pal-input">
        <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="#8e8e8e" stroke-width="2.2" stroke-linecap="round"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
        <input placeholder="Search title, goal, outcome, repo, branch, filename" />
      </div>
      <div class="pal-scope"></div>
      <div class="pal-body"></div>
      <div class="pal-foot">
        <span><span class="kbd">↑↓</span> move</span>
        <span><span class="kbd">↵</span> open</span>
        <span><span class="kbd">esc</span> close</span>
        <span style="margin-left:auto" class="pal-target"></span>
      </div>
    </div>`;
  document.body.appendChild(scrim);
  scrim.addEventListener('click', (e) => { if (e.target === scrim) closePalette(); });
  const input = scrim.querySelector('input');
  input.addEventListener('input', () => { _palIndex = 0; renderPalette(input.value); });
  input.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') { e.preventDefault(); _palIndex = Math.min(_palIndex + 1, _palRows.length - 1); renderPalette(input.value, true); }
    else if (e.key === 'ArrowUp') { e.preventDefault(); _palIndex = Math.max(_palIndex - 1, 0); renderPalette(input.value, true); }
    else if (e.key === 'Enter') {
      e.preventDefault();
      const row = _palRows[_palIndex];
      if (row) { closePalette(); openSession(row.id); }
    } else if (e.key === 'Escape') { closePalette(); }
  });
  return scrim;
}

function openPalette() {
  if (!_pal) _pal = mountPalette();
  _pal.classList.add('open');
  const input = _pal.querySelector('input');
  input.value = '';
  _palIndex = 0;
  renderPalette('');
  input.focus();
}
function closePalette() { if (_pal) _pal.classList.remove('open'); }

function renderPalette(query, keepScroll) {
  const body = _pal.querySelector('.pal-body');
  const scope = _pal.querySelector('.pal-scope');
  const target = _pal.querySelector('.pal-target');
  const q = query.trim();
  const prevScroll = body.scrollTop;

  if (!q) {
    // Empty query: shortcuts are appropriate. Live sessions surface here, not in ranked results.
    const live = SESSIONS.filter((s) => s.status === 'busy' || s.status === 'waiting');
    const recent = SESSIONS.filter((s) => s.status !== 'busy' && s.status !== 'waiting').slice(0, 4);
    _palRows = [...live, ...recent];
    scope.innerHTML = `Searching <b>all time</b> · ${TOTALS.sessions} sessions · <b>${TOTALS.waiting} waiting</b>, ${TOTALS.active} running`;
    body.innerHTML =
      `<div class="pal-group">Live now <span>· ${live.length}</span></div>` +
      live.map((s, i) => palRow(s, null, q, i)).join('') +
      `<div class="pal-group">Recently active</div>` +
      recent.map((s, i) => palRow(s, null, q, live.length + i)).join('');
  } else {
    const hits = searchSessions(q);
    _palRows = hits.map((h) => h.session);
    const pending = 3;
    scope.innerHTML = `Searching <b>all time</b> · <b>${hits.length} ${hits.length === 1 ? 'match' : 'matches'}</b> of ${TOTALS.sessions} sessions`;
    if (!hits.length) {
      body.innerHTML = `<div class="pal-empty">
        No session matches “${escapeHtml(q)}” in title, goal, outcome, repo, branch or filename.<br>
        <span style="font-size:11.5px">${pending} sessions have no synthesis yet and cannot be matched on goal or outcome.</span>
      </div>`;
    } else {
      body.innerHTML =
        `<div class="pal-group">Matches <span>· ranked by where the text was found; recency breaks ties</span></div>` +
        hits.map((h, i) => palRow(h.session, h.evidence, q, i)).join('') +
        `<div class="pal-group">Not searched <span>· ${pending} sessions have no synthesis yet</span></div>`;
    }
  }

  const sel = _palRows[_palIndex];
  target.innerHTML = sel
    ? `<span class="kbd">↵</span> opens in <b style="color:#45484f">${agentOf(sel).label} · ${sel.source === 'local' ? 'This Mac' : sel.source}</b>`
    : '';

  body.querySelectorAll('.pal-row').forEach((row, i) => {
    row.addEventListener('click', () => { closePalette(); openSession(row.dataset.id); });
    row.addEventListener('mousemove', () => { if (_palIndex !== i) { _palIndex = i; renderPalette(query, true); } });
  });
  if (keepScroll) body.scrollTop = prevScroll;
  const active = body.querySelector('[aria-selected="true"]');
  if (active && !keepScroll) active.scrollIntoView({ block: 'nearest' });
}

function palRow(s, evidence, q, index) {
  const sel = index === _palIndex;
  let sub;
  if (evidence && evidence.field !== 'title') {
    const text = snippet(evidence.value, evidence.index, q);
    const extra = evidence.field === 'repo' && /^[a-z-]+-https?-/.test(s.repo)
      ? ' <span style="color:#b0b3b9">· generated folder name</span>' : '';
    sub = `<span class="why">${evidence.field}</span>${highlight(text, q)}${extra}`;
  } else {
    sub = `${escapeHtml(s.repo)}${s.branch ? ' · ' + escapeHtml(s.branch) : ''} · ${escapeHtml(s.when)}`;
  }
  const title = evidence && evidence.field === 'title' ? highlight(s.title, q) : escapeHtml(s.title);
  return `<div class="pal-row" role="option" aria-selected="${sel}" data-id="${s.id}">
    <span class="agent-tag agent-${s.agent}" style="font-size:10px"><span class="agent-swatch"></span>${agentOf(s).label}</span>
    <div class="pal-main">
      <div class="pal-title">${title}</div>
      <div class="pal-sub">${sub}</div>
    </div>
    <div class="pal-right">${money(s.cost)}<br><span style="font-size:10px">${escapeHtml(s.when)}</span></div>
  </div>`;
}

/* ---------- global keys ---------- */
document.addEventListener('keydown', (e) => {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') { e.preventDefault(); openPalette(); return; }
  if (e.key === 'Escape') { closePalette(); closeDrawer(); }
  if (e.key === '/' && !/^(INPUT|TEXTAREA)$/.test(document.activeElement.tagName)) {
    e.preventDefault();
    const box = document.querySelector('.search-wrap input');
    if (box) box.focus(); else openPalette();
  }
});

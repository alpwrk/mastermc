'use strict';

// ---------- Helpers ----------
const $ = (s) => document.querySelector(s);
const $$ = (s) => document.querySelectorAll(s);

// h builds DOM elements safely (no innerHTML with user data).
function h(tag, attrs = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (v === true) el.setAttribute(k, '');
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c != null) el.append(c instanceof Node ? c : String(c));
  }
  return el;
}

// Line icons (Lucide style), built as inline SVG.
const ICONS = {
  folder: ['M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z'],
  file: ['M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z', 'M14 2v4a2 2 0 0 0 2 2h4'],
  download: ['M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4', 'M7 10l5 5 5-5', 'M12 15V3'],
  upload: ['M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4', 'M17 8l-5-5-5 5', 'M12 3v12'],
  edit: ['M12 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7', 'M18.375 2.625a1 1 0 0 1 3 3l-9.013 9.014a2 2 0 0 1-.853.505l-2.873.84a.5.5 0 0 1-.62-.62l.84-2.873a2 2 0 0 1 .506-.852z'],
  rename: ['M12 20h9', 'M16.376 3.622a1 1 0 0 1 3.002 3.002L7.368 18.635a2 2 0 0 1-.855.506l-2.872.838a.5.5 0 0 1-.62-.62l.838-2.872a2 2 0 0 1 .506-.854z'],
  trash: ['M3 6h18', 'M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6', 'M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2'],
  extract: ['M3 3h18a1 1 0 0 1 1 1v3a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1z', 'M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8', 'M10 12h4'],
  up: ['M14 9 9 4 4 9', 'M20 20h-7a4 4 0 0 1-4-4V4'],
  plus: ['M5 12h14', 'M12 5v14'],
  refresh: ['M21 12a9 9 0 1 1-9-9c2.52 0 4.93 1 6.74 2.74L21 8', 'M21 3v5h-5'],
  play: ['M6 3l14 9-14 9V3z'],
  stop: ['M5 3h14a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z'],
  x: ['M18 6 6 18', 'M6 6l12 12'],
};
function icon(name) {
  const NS = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(NS, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('class', 'ic');
  svg.setAttribute('aria-hidden', 'true');
  for (const d of ICONS[name] || []) {
    const path = document.createElementNS(NS, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}
$$('[data-icon]').forEach((el) => el.prepend(icon(el.dataset.icon)));

async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(url, opts);
  if (res.status === 401) {
    location.href = '/login';
    throw new Error('not logged in');
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || `HTTP ${res.status}`);
  return data;
}

function toast(msg, type = 'ok') {
  const t = h('div', { class: 'toast ' + type }, msg);
  $('#toasts').append(t);
  setTimeout(() => t.remove(), type === 'error' ? 7000 : 3500);
}

async function run(fn, okMsg) {
  try {
    const r = await fn();
    if (okMsg) toast(okMsg);
    return r;
  } catch (e) {
    toast(e.message, 'error');
  }
}

function fmtSize(n) {
  if (n < 1024) return n + ' B';
  const u = ['KB', 'MB', 'GB', 'TB'];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + ' ' + u[i];
}
const fmtDate = (s) => new Date(s * 1000).toLocaleString('en-GB', { dateStyle: 'short', timeStyle: 'short' });
function fmtDuration(sec) {
  const d = Math.floor(sec / 86400), hh = Math.floor(sec % 86400 / 3600), m = Math.floor(sec % 3600 / 60);
  return (d ? d + 'd ' : '') + (d || hh ? hh + 'h ' : '') + m + 'm';
}
const joinPath = (a, b) => (a ? a.replace(/\/$/, '') + '/' : '') + b;

// Dialogs (replacement for prompt/confirm)
function ask(msg, { input = false, value = '', okText = 'OK', danger = false } = {}) {
  const dlg = $('#dialog'), inp = $('#dialog-input'), okBtn = $('#dialog-ok');
  $('#dialog-msg').textContent = msg;
  inp.hidden = !input;
  inp.value = value;
  okBtn.textContent = okText;
  okBtn.className = 'btn ' + (danger ? 'danger' : 'primary');
  dlg.returnValue = '';
  dlg.showModal();
  if (input) {
    inp.focus();
    const dot = value.lastIndexOf('.');
    inp.setSelectionRange(0, dot > 0 ? dot : value.length);
  }
  return new Promise((resolve) => {
    dlg.addEventListener('close', () => {
      if (dlg.returnValue !== 'ok') resolve(null);
      else resolve(input ? inp.value.trim() : true);
    }, { once: true });
  });
}

// ---------- Tabs ----------
const tabLoaders = {};
function showTab(name) {
  $$('#nav button').forEach((b) => b.classList.toggle('active', b.dataset.tab === name));
  $$('.tab').forEach((t) => t.classList.toggle('active', t.id === 'tab-' + name));
  history.replaceState(null, '', '#' + name);
  if (tabLoaders[name]) tabLoaders[name]();
  if (name === 'console') scrollConsole(true);
}
$('#nav').addEventListener('click', (e) => {
  if (e.target.dataset.tab) showTab(e.target.dataset.tab);
});
document.addEventListener('click', (e) => {
  const g = e.target.closest('[data-goto]');
  if (g) { e.preventDefault(); showTab(g.dataset.goto); }
});
$('#logout').addEventListener('click', async () => {
  await fetch('/api/logout', { method: 'POST' });
  location.href = '/login';
});

// ---------- Console & live events ----------
const MAX_LINES = 2000;
const consoleEl = $('#console');
const miniEl = $('#mini-console');

function lineClass(t) {
  if (t.startsWith('[panel]')) return 'l-panel';
  if (t.startsWith('> ')) return 'l-cmd';
  if (/\b(ERROR|SEVERE|FATAL)\b|Exception/.test(t)) return 'l-error';
  if (/\bWARN(ING)?\b/.test(t)) return 'l-warn';
  return '';
}
// eslint-disable-next-line no-control-regex
const stripAnsi = (s) => s.replace(/\x1b\[[0-9;?]*[A-Za-z]/g, '').replace(/§[0-9a-fk-or]/gi, '');

function nearBottom(el) { return el.scrollHeight - el.scrollTop - el.clientHeight < 60; }
function scrollConsole(force) {
  if (force || $('#autoscroll').checked) consoleEl.scrollTop = consoleEl.scrollHeight;
}

function appendLines(lines) {
  const stick = nearBottom(consoleEl);
  const frag = document.createDocumentFragment();
  for (const l of lines) {
    const text = stripAnsi(l.text);
    frag.append(h('div', { class: lineClass(text) }, text));
  }
  consoleEl.append(frag);
  while (consoleEl.childElementCount > MAX_LINES) consoleEl.firstElementChild.remove();
  if (stick) scrollConsole();
  // Mini console on the dashboard: last 12 lines
  const last = Array.from(consoleEl.children).slice(-12);
  miniEl.replaceChildren(...last.map((d) => d.cloneNode(true)));
  miniEl.scrollTop = miniEl.scrollHeight;
}
$('#console-clear').addEventListener('click', () => { consoleEl.replaceChildren(); });

const stateNames = { stopped: 'Stopped', starting: 'Starting …', running: 'Running', stopping: 'Stopping …' };
let serverState = 'stopped';
function setState(st) {
  serverState = st;
  const pill = $('#state-pill');
  pill.className = 'pill ' + st;
  pill.textContent = stateNames[st] || st;
  $('#d-state').textContent = stateNames[st] || st;
  $$('[data-action]').forEach((b) => {
    const a = b.dataset.action;
    b.disabled = (a === 'start' && st !== 'stopped') || (a !== 'start' && a !== 'restart' && st === 'stopped');
  });
}

let lastTask = {};
function setTask(t) {
  t = t || {};
  const banner = $('#task-banner');
  const wasRunning = lastTask.running;
  lastTask = t;
  if (!t.running && !t.error && !wasRunning) {
    banner.hidden = true;
    return;
  }
  banner.hidden = false;
  banner.classList.toggle('error', !!t.error);
  $('#task-name').textContent = t.name || '';
  $('#task-msg').textContent = t.error ? '– ' + t.error : (t.message ? '– ' + t.message : '');
  const bar = $('#task-bar'), prog = bar.parentElement;
  const indet = t.running && !(t.progress >= 0);
  prog.classList.toggle('indeterminate', indet);
  bar.style.width = indet ? '' : Math.max(0, Math.min(100, t.progress || 0)) + '%';
  $('#task-close').hidden = t.running;
  if (wasRunning && !t.running) {
    if (t.error) toast(t.name + ' failed: ' + t.error, 'error');
    else {
      toast(t.name + ' completed');
      setTimeout(() => { if (!lastTask.running && !lastTask.error) banner.hidden = true; }, 4000);
    }
    refreshStatus();
    if ($('#tab-java').classList.contains('active')) loadJava();
    if ($('#tab-files').classList.contains('active')) loadFiles();
  }
}
$('#task-close').addEventListener('click', () => { $('#task-banner').hidden = true; });

function connectEvents() {
  const es = new EventSource('/api/events');
  es.onmessage = (e) => {
    const ev = JSON.parse(e.data);
    switch (ev.type) {
      case 'backlog': consoleEl.replaceChildren(); appendLines(ev.data || []); scrollConsole(true); break;
      case 'log': appendLines([ev.data]); break;
      case 'state': setState(ev.data); refreshStatus(); break;
      case 'task': setTask(ev.data); break;
    }
  };
  es.onerror = () => {
    // On an expired session go to the login page, otherwise EventSource reconnects by itself.
    fetch('/api/status').then((r) => { if (r.status === 401) location.href = '/login'; });
  };
}

// Commands with history
const history_ = [];
let histPos = 0;
$('#cmd-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const inp = $('#cmd');
  const cmd = inp.value.trim().replace(/^\//, '');
  if (!cmd) return;
  history_.push(cmd);
  histPos = history_.length;
  inp.value = '';
  await run(() => api('POST', '/api/command', { command: cmd }));
  scrollConsole(true);
});
$('#cmd').addEventListener('keydown', (e) => {
  if (e.key === 'ArrowUp' && histPos > 0) { histPos--; e.target.value = history_[histPos]; e.preventDefault(); }
  if (e.key === 'ArrowDown') {
    histPos = Math.min(history_.length, histPos + 1);
    e.target.value = history_[histPos] || '';
    e.preventDefault();
  }
});

// ---------- Dashboard ----------
const typeNames = { vanilla: 'Vanilla', paper: 'Paper', custom: 'Custom jar' };
async function refreshStatus() {
  let s;
  try { s = await api('GET', '/api/status'); } catch { return; }
  const srv = s.server;
  setState(srv.state);
  $('#d-type').textContent = s.installed ? (typeNames[s.serverType] || 'Server') + (s.mcVersion ? ' ' + s.mcVersion : '') : 'not installed';
  $('#d-cpu').textContent = srv.pid ? srv.cpu.toFixed(0) + ' %' : '–';
  $('#d-mem').textContent = srv.pid ? fmtSize(srv.memoryMb * 1048576) : '–';
  $('#d-uptime').textContent = srv.startedAt ? fmtDuration(Date.now() / 1000 - srv.startedAt) : '–';
  $('#d-port').textContent = s.port;
  $('#d-host').textContent = `Host: ${s.host.cpus} CPU cores · ${fmtSize(s.host.memAvailMb * 1048576)} of ${fmtSize(s.host.memTotalMb * 1048576)} RAM free · ${s.host.arch}`;
  $('#d-java').textContent = srv.java ? 'Java: ' + srv.java : '';
  $('#d-not-installed').hidden = s.installed;
  $('#pw-warning').hidden = !s.initialPassword;
}
$$('[data-action]').forEach((b) => b.addEventListener('click', async () => {
  const a = b.dataset.action;
  if (a === 'kill' && !await ask('Really force-kill the server? Unsaved data may be lost.', { okText: 'Kill', danger: true })) return;
  await run(() => api('POST', '/api/server/' + a));
  refreshStatus();
}));

// ---------- Files ----------
let cwd = '';
let entries = [];
const selected = new Set();
const BINARY_EXT = /\.(jar|zip|gz|tgz|xz|7z|rar|dat|dat_old|mca|mcr|nbt|png|jpg|jpeg|gif|webp|ico|db|sqlite|so|dll|exe|class|lock)$/i;

tabLoaders.files = () => loadFiles();

async function loadFiles(path = cwd) {
  try {
    entries = await api('GET', '/api/files/list?path=' + encodeURIComponent(path));
  } catch (e) {
    toast(e.message, 'error');
    if (path !== '') return loadFiles('');
    return;
  }
  cwd = path;
  selected.clear();
  renderFiles();
}

function renderBreadcrumb() {
  const bc = $('#breadcrumb');
  const parts = cwd.split('/').filter(Boolean);
  const items = [h('a', { href: '#', onclick: (e) => { e.preventDefault(); loadFiles(''); } }, 'server')];
  parts.forEach((p, i) => {
    const target = parts.slice(0, i + 1).join('/');
    items.push(h('span', {}, '/'), h('a', { href: '#', onclick: (e) => { e.preventDefault(); loadFiles(target); } }, p));
  });
  bc.replaceChildren(...items);
}

function renderFiles() {
  renderBreadcrumb();
  const rows = [];
  if (cwd) {
    rows.push(h('tr', {},
      h('td'),
      h('td', {}, h('span', { class: 'name', onclick: () => loadFiles(cwd.split('/').slice(0, -1).join('/')) },
        h('span', { class: 'icon dir' }, icon('up')), '..')),
      h('td'), h('td'), h('td')));
  }
  for (const e of entries) {
    const path = joinPath(cwd, e.name);
    const cb = h('input', { type: 'checkbox', onchange: (ev) => { ev.target.checked ? selected.add(e.name) : selected.delete(e.name); updateSelection(); } });
    const open = () => e.dir ? loadFiles(path) : openEditor(path, e);
    const actions = [];
    if (!e.dir) actions.push(h('button', { class: 'icon-btn', title: 'Edit', onclick: () => openEditor(path, e) }, icon('edit')));
    if (/\.zip$/i.test(e.name)) actions.push(h('button', { class: 'icon-btn', title: 'Extract', onclick: () => unzip(path) }, icon('extract')));
    actions.push(
      h('button', { class: 'icon-btn', title: e.dir ? 'Download as ZIP' : 'Download', onclick: () => download([e.name]) }, icon('download')),
      h('button', { class: 'icon-btn', title: 'Rename / move', onclick: () => rename(e.name) }, icon('rename')),
      h('button', { class: 'icon-btn danger', title: 'Delete', onclick: () => del([e.name]) }, icon('trash')),
    );
    rows.push(h('tr', {},
      h('td', { class: 'c-check' }, cb),
      h('td', {}, h('span', { class: 'name', onclick: open },
        h('span', { class: 'icon' + (e.dir ? ' dir' : '') }, icon(e.dir ? 'folder' : 'file')), e.name + (e.link ? ' ↪' : ''))),
      h('td', { class: 'c-size' }, e.dir ? '' : fmtSize(e.size)),
      h('td', { class: 'c-date' }, fmtDate(e.modTime)),
      h('td', { class: 'c-actions' }, actions)));
  }
  if (!entries.length) rows.push(h('tr', {}, h('td', { colspan: 5, class: 'muted' }, 'Folder is empty')));
  $('#file-list').replaceChildren(...rows);
  $('#sel-all').checked = false;
  updateSelection();
}

function updateSelection() {
  $('#selection-bar').hidden = selected.size === 0;
  $('#sel-count').textContent = selected.size + ' selected';
}
$('#sel-all').addEventListener('change', (e) => {
  $$('#file-list input[type=checkbox]').forEach((cb) => { cb.checked = e.target.checked; });
  selected.clear();
  if (e.target.checked) entries.forEach((en) => selected.add(en.name));
  updateSelection();
});

function download(names) {
  const q = new URLSearchParams({ path: cwd });
  names.forEach((n) => q.append('name', n));
  const a = h('a', { href: '/api/files/download?' + q, download: '' });
  document.body.append(a);
  a.click();
  a.remove();
}

async function del(names) {
  const msg = names.length === 1 ? `Really delete “${names[0]}”?` : `Really delete ${names.length} items?`;
  if (!await ask(msg + '\nThis cannot be undone.', { okText: 'Delete', danger: true })) return;
  await run(() => api('POST', '/api/files/delete', { paths: names.map((n) => joinPath(cwd, n)) }), 'Deleted');
  loadFiles();
}

async function rename(name) {
  const to = await ask('New name (or path relative to the current folder):', { input: true, value: name });
  if (!to || to === name) return;
  const target = to.startsWith('/') ? to : joinPath(cwd, to);
  await run(() => api('POST', '/api/files/rename', { path: joinPath(cwd, name), to: target }), 'Renamed');
  loadFiles();
}

async function unzip(path) {
  const dest = await ask('Extract to (folder):', { input: true, value: cwd });
  if (dest === null) return;
  await run(() => api('POST', '/api/files/unzip', { path, to: dest }), 'Extracted');
  loadFiles();
}

$('#f-refresh').addEventListener('click', () => loadFiles());
$('#f-dl-sel').addEventListener('click', () => download([...selected]));
$('#f-del-sel').addEventListener('click', () => del([...selected]));
$('#f-newdir').addEventListener('click', async () => {
  const name = await ask('Name of the new folder:', { input: true });
  if (!name) return;
  await run(() => api('POST', '/api/files/mkdir', { path: joinPath(cwd, name) }));
  loadFiles();
});
$('#f-newfile').addEventListener('click', async () => {
  const name = await ask('Name of the new file:', { input: true });
  if (!name) return;
  const path = joinPath(cwd, name);
  if (entries.some((e) => e.name === name)) return toast('File already exists', 'error');
  if (await run(() => api('PUT', '/api/files/write', { path, content: '' }))) {
    await loadFiles();
    openEditor(path, { name, size: 0 });
  }
});

// Uploads: one request per file, with progress
$('#f-upload').addEventListener('click', () => $('#file-input').click());
$('#f-upload-dir').addEventListener('click', () => $('#dir-input').click());
$('#file-input').addEventListener('change', (e) => { uploadFiles([...e.target.files].map((f) => [f, f.name])); e.target.value = ''; });
$('#dir-input').addEventListener('change', (e) => { uploadFiles([...e.target.files].map((f) => [f, f.webkitRelativePath || f.name])); e.target.value = ''; });

function uploadOne(file, rel, dir) {
  return new Promise((resolve) => {
    const bar = h('div');
    const item = h('div', { class: 'upload-item' }, h('span', { class: 'uname' }, rel), h('div', { class: 'progress' }, bar));
    $('#uploads').append(item);
    const xhr = new XMLHttpRequest();
    xhr.open('PUT', '/api/files/upload?path=' + encodeURIComponent(joinPath(dir, rel)));
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) bar.style.width = (e.loaded / e.total * 100) + '%'; };
    xhr.onload = () => {
      item.remove();
      if (xhr.status !== 200) {
        let msg = 'HTTP ' + xhr.status;
        try { msg = JSON.parse(xhr.responseText).error; } catch { /* ignore */ }
        toast(rel + ': ' + msg, 'error');
        resolve(false);
      } else resolve(true);
    };
    xhr.onerror = () => { item.remove(); toast(rel + ': upload failed', 'error'); resolve(false); };
    xhr.send(file);
  });
}

async function uploadFiles(list) {
  if (!list.length) return;
  const dir = cwd;
  let ok = 0;
  // Max. 3 parallel uploads
  const queue = [...list];
  const worker = async () => {
    while (queue.length) {
      const [f, rel] = queue.shift();
      if (await uploadOne(f, rel, dir)) ok++;
    }
  };
  await Promise.all([worker(), worker(), worker()]);
  if (ok) toast(`${ok} file(s) uploaded`);
  if (cwd === dir) loadFiles();
}

// Drag & drop (including folders)
const filesCard = $('#files-card');
let dragDepth = 0;
filesCard.addEventListener('dragenter', (e) => { e.preventDefault(); dragDepth++; filesCard.classList.add('dragover'); });
filesCard.addEventListener('dragleave', () => { if (--dragDepth <= 0) { dragDepth = 0; filesCard.classList.remove('dragover'); } });
filesCard.addEventListener('dragover', (e) => e.preventDefault());
filesCard.addEventListener('drop', async (e) => {
  e.preventDefault();
  dragDepth = 0;
  filesCard.classList.remove('dragover');
  const items = [...e.dataTransfer.items].map((i) => i.webkitGetAsEntry && i.webkitGetAsEntry()).filter(Boolean);
  const out = [];
  const walk = async (entry, prefix) => {
    if (entry.isFile) {
      const f = await new Promise((res, rej) => entry.file(res, rej));
      out.push([f, prefix + entry.name]);
    } else if (entry.isDirectory) {
      const reader = entry.createReader();
      let batch;
      do {
        batch = await new Promise((res, rej) => reader.readEntries(res, rej));
        for (const c of batch) await walk(c, prefix + entry.name + '/');
      } while (batch.length);
    }
  };
  if (items.length) for (const it of items) await walk(it, '');
  else [...e.dataTransfer.files].forEach((f) => out.push([f, f.name]));
  uploadFiles(out);
});

// ---------- Editor ----------
let editorPath = null;
let editorDirty = false;
async function openEditor(path, entry) {
  if (BINARY_EXT.test(entry.name)) {
    download([entry.name]);
    return;
  }
  let data;
  try {
    data = await api('GET', '/api/files/read?path=' + encodeURIComponent(path));
  } catch (e) {
    toast(e.message, 'error');
    return;
  }
  editorPath = path;
  editorDirty = false;
  $('#editor-title').textContent = path;
  $('#editor-status').textContent = '';
  $('#editor-text').value = data.content;
  $('#editor').showModal();
  $('#editor-text').focus();
  $('#editor-text').setSelectionRange(0, 0);
  $('#editor-text').scrollTop = 0;
}
async function saveEditor() {
  const r = await run(() => api('PUT', '/api/files/write', { path: editorPath, content: $('#editor-text').value }));
  if (r) {
    editorDirty = false;
    $('#editor-status').textContent = 'Saved ' + new Date().toLocaleTimeString('en-GB');
    loadFiles();
  }
}
$('#editor-save').addEventListener('click', saveEditor);
$('#editor-text').addEventListener('input', () => { editorDirty = true; $('#editor-status').textContent = 'Unsaved'; });
async function closeEditor() {
  if (editorDirty && !await ask('Discard unsaved changes?', { okText: 'Discard', danger: true })) return;
  editorDirty = false;
  $('#editor').close();
}
$('#editor-close').addEventListener('click', closeEditor);
$('#editor').addEventListener('cancel', (e) => { e.preventDefault(); closeEditor(); });
$('#editor').addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key === 's') { e.preventDefault(); saveEditor(); }
  if (e.key === 'Tab' && e.target.id === 'editor-text') {
    e.preventDefault();
    document.execCommand('insertText', false, '  ');
  }
});

// ---------- Install ----------
let instType = 'vanilla';
const versionCache = {};
tabLoaders.install = () => loadVersions();

async function loadVersions() {
  if (instType === 'custom') return;
  const snaps = instType === 'vanilla' && $('#inst-snapshots').checked;
  const key = instType + (snaps ? '-s' : '');
  const sel = $('#inst-version');
  if (!versionCache[key]) {
    sel.replaceChildren(h('option', {}, 'Loading …'));
    try {
      versionCache[key] = await api('GET', `/api/versions/${instType}${snaps ? '?snapshots=1' : ''}`);
    } catch (e) {
      sel.replaceChildren(h('option', { value: '' }, 'Error: ' + e.message));
      return;
    }
  }
  sel.replaceChildren(...versionCache[key].map((v, i) =>
    h('option', { value: v.id }, v.id + (v.type === 'snapshot' ? ' (snapshot)' : '') + (i === 0 ? ' – latest' : ''))));
}
$('#inst-type').addEventListener('click', (e) => {
  const t = e.target.dataset.type;
  if (!t) return;
  instType = t;
  $$('#inst-type button').forEach((b) => b.classList.toggle('active', b.dataset.type === t));
  $('#inst-version-row').hidden = t === 'custom';
  $('#inst-snap-row').hidden = t !== 'vanilla';
  $('#inst-url-row').hidden = t !== 'custom';
  loadVersions();
});
$('#inst-snapshots').addEventListener('change', loadVersions);
$('#inst-go').addEventListener('click', async () => {
  const req = {
    type: instType,
    version: instType === 'custom' ? '' : $('#inst-version').value,
    url: $('#inst-url').value.trim(),
    javaVersion: Number($('#inst-java').value),
    eula: $('#inst-eula').checked,
    wipe: $('#inst-wipe').checked,
  };
  if (!req.eula) return toast('Please accept the Minecraft EULA', 'error');
  if (instType === 'custom' && !req.url) return toast('Please enter a download URL', 'error');
  if (req.wipe && !await ask('The entire server directory (worlds, configs, plugins) will be deleted. Continue?', { okText: 'Wipe & install', danger: true })) return;
  if (serverState !== 'stopped' && !await ask('The running server will be stopped for this. Continue?', { okText: 'Continue' })) return;
  if (await run(() => api('POST', '/api/install', req))) showTab('console');
});

// ---------- Java ----------
tabLoaders.java = () => loadJava();
async function loadJava() {
  let d;
  try { d = await api('GET', '/api/java'); } catch (e) { return toast(e.message, 'error'); }
  const mode = $('#java-mode');
  mode.replaceChildren(
    h('option', { value: 'auto' }, `Automatic (required: Java ${d.required})`),
    ...d.installed.map((j) => h('option', { value: j.path }, `Java ${j.version} – ${j.path}`)));
  mode.value = d.mode || 'auto';
  if (mode.value !== (d.mode || 'auto')) mode.append(h('option', { value: d.mode, selected: true }, d.mode + ' (not found)'));

  const rows = d.installed.map((j) => h('tr', {},
    h('td', {}, 'Java ' + j.version),
    h('td', { class: 'path' }, j.path),
    h('td', {}, h('span', { class: 'badge' + (j.managed ? ' green' : '') }, j.managed ? 'Panel' : 'System')),
    h('td', { class: 'c-actions' },
      h('button', { class: 'btn small', onclick: () => setJavaMode(j.path) }, 'Use'), ' ',
      j.managed ? h('button', { class: 'btn small danger', onclick: () => deleteJava(j) }, 'Delete') : null)));
  if (!rows.length) rows.push(h('tr', {}, h('td', { colspan: 4, class: 'muted' }, 'No Java installation found – it will be installed automatically on first start.')));
  $('#java-list').replaceChildren(...rows);

  const major = $('#java-major');
  if (d.available) {
    const lts = new Set(d.available.lts);
    major.replaceChildren(...[...d.available.releases].reverse().map((v) =>
      h('option', { value: v, selected: v === d.required }, `Java ${v}${lts.has(v) ? ' (LTS)' : ''}`)));
  } else {
    major.replaceChildren(...[25, 21, 17, 11, 8].map((v) => h('option', { value: v }, 'Java ' + v)));
    toast('Adoptium not reachable: ' + d.availableError, 'error');
  }
}
async function setJavaMode(value) {
  const s = await run(() => api('GET', '/api/settings'));
  if (!s) return;
  s.javaMode = value;
  if (await run(() => api('PUT', '/api/settings', s), 'Java setting saved – takes effect on next start')) loadJava();
}
$('#java-mode-save').addEventListener('click', () => setJavaMode($('#java-mode').value));
async function deleteJava(j) {
  if (!await ask(`Delete Java ${j.version}?\n${j.path}`, { okText: 'Delete', danger: true })) return;
  await run(() => api('POST', '/api/java/delete', { path: j.path }), 'Java deleted');
  loadJava();
}
$('#java-install').addEventListener('click', () => run(() => api('POST', '/api/java/install', {
  major: Number($('#java-major').value),
  imageType: $('#java-image').value,
})));

// ---------- Settings ----------
const AIKAR = '-XX:+UseG1GC -XX:+ParallelRefProcEnabled -XX:MaxGCPauseMillis=200 -XX:+UnlockExperimentalVMOptions -XX:+DisableExplicitGC -XX:+AlwaysPreTouch -XX:G1NewSizePercent=30 -XX:G1MaxNewSizePercent=40 -XX:G1HeapRegionSize=8M -XX:G1ReservePercent=20 -XX:G1HeapWastePercent=5 -XX:G1MixedGCCountTarget=4 -XX:InitiatingHeapOccupancyPercent=15 -XX:G1MixedGCLiveThresholdPercent=90 -XX:G1RSetUpdatingPauseIntervalMillis=100 -XX:SurvivorRatio=32 -XX:+PerfDisableSharedMem -XX:MaxTenuringThreshold=1 -Dusing.aikars.flags=https://mcflags.emc.gs -Daikars.new.flags=true';
let settings = null;
tabLoaders.settings = async () => {
  settings = await run(() => api('GET', '/api/settings'));
  if (!settings) return;
  $('#s-minram').value = settings.minRam;
  $('#s-maxram').value = settings.maxRam;
  $('#s-jvmargs').value = settings.jvmArgs;
  $('#s-serverargs').value = settings.serverArgs;
  $('#s-jar').value = settings.jarName;
  $('#s-autostart').checked = settings.autostart;
  $('#s-autorestart').checked = settings.autoRestart;
};
$('#s-aikar').addEventListener('click', () => { $('#s-jvmargs').value = AIKAR; });
$('#settings-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  const s = await run(() => api('GET', '/api/settings'));
  if (!s) return;
  Object.assign(s, {
    minRam: $('#s-minram').value.trim(),
    maxRam: $('#s-maxram').value.trim(),
    jvmArgs: $('#s-jvmargs').value.trim(),
    serverArgs: $('#s-serverargs').value.trim(),
    jarName: $('#s-jar').value.trim(),
    autostart: $('#s-autostart').checked,
    autoRestart: $('#s-autorestart').checked,
  });
  await run(() => api('PUT', '/api/settings', s), 'Saved – takes effect on next start');
});
$('#pw-form').addEventListener('submit', async (e) => {
  e.preventDefault();
  if ($('#pw-new').value !== $('#pw-new2').value) return toast('The new passwords do not match', 'error');
  if (await run(() => api('POST', '/api/password', { old: $('#pw-old').value, new: $('#pw-new').value }), 'Password changed')) {
    e.target.reset();
    refreshStatus();
  }
});

// ---------- Start ----------
connectEvents();
refreshStatus();
setInterval(() => { if (!document.hidden) refreshStatus(); }, 3000);
const initial = location.hash.slice(1);
showTab($('#tab-' + initial) ? initial : 'dashboard');

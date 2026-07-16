"use strict";

const api = () => window.go.main.App;

const state = {
  profiles: [],
  selection: new Set(),
  statuses: {}, // id -> "running" | "success" | "failed" | "partial"
  busy: false,
  editingId: null,
  sortMode: localStorage.getItem("sortMode") || "tag", // "tag" | "manual"
  logOpen: false,
};

const $ = (id) => document.getElementById(id);

// ---- helpers ---------------------------------------------------------------

// Profiles may come from a 1.x file (single source/destination) or the new
// schema (sources/destinations arrays); normalize to arrays everywhere.
function normalize(p) {
  if (!Array.isArray(p.sources) || p.sources.length === 0) {
    p.sources = p.source ? [p.source] : [];
  }
  if (!Array.isArray(p.destinations) || p.destinations.length === 0) {
    p.destinations = p.destination ? [p.destination] : [];
  }
  return p;
}

function setProfiles(list) {
  state.profiles = (list || []).map(normalize);
}

function tags() {
  const seen = new Set();
  for (const p of state.profiles) if (p.tag) seen.add(p.tag);
  return [...seen].sort((a, b) => a.localeCompare(b));
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

let bannerTimer = null;
function showBanner(msg, isError) {
  const b = $("banner");
  b.textContent = msg;
  b.className = "banner" + (isError ? " error" : "");
  b.hidden = false;
  clearTimeout(bannerTimer);
  bannerTimer = setTimeout(() => { b.hidden = true; }, 6000);
}

// ---- rendering --------------------------------------------------------------

function render() {
  renderChips();
  renderProfiles();
  $("btn-remove").disabled = state.selection.size === 0;
  $("btn-verify").disabled = state.busy;
  const st = $("status-text");
  st.textContent = state.busy ? "Sincronizzazione in corso…" : "Pronto";
  st.classList.toggle("busy", state.busy);
  for (const b of $("sort-toggle").querySelectorAll("button")) {
    b.classList.toggle("active", b.dataset.mode === state.sortMode);
  }
}

function chip(label, count, title, extraClass, runFn) {
  const el = document.createElement("span");
  el.className = "chip" + (extraClass ? " " + extraClass : "");
  el.innerHTML = `${escapeHtml(label)} <span class="count">${count}</span>`;
  const play = document.createElement("button");
  play.title = title;
  play.disabled = state.busy;
  play.innerHTML = `<svg viewBox="0 0 12 12"><path d="M3 1.5l7 4.5-7 4.5z"/></svg>`;
  play.addEventListener("click", async () => {
    openLog();
    try { await runFn(); } catch (e) { showBanner(String(e), true); }
  });
  el.appendChild(play);
  return el;
}

function renderChips() {
  const box = $("tag-chips");
  box.innerHTML = "";
  for (const tag of tags()) {
    const count = state.profiles.filter((p) => p.tag === tag).length;
    box.appendChild(chip(
      tag, count,
      `Avvia in sequenza tutte le destinazioni con tag "${tag}"`,
      "",
      () => api().RunTag(tag)
    ));
  }
  const untagged = state.profiles.filter((p) => !p.tag).length;
  if (untagged > 0) {
    box.appendChild(chip(
      "Senza tag", untagged,
      "Avvia in sequenza tutte le destinazioni senza tag",
      "chip-untagged",
      () => api().RunUntagged()
    ));
  }
}

// Build the display order. "tag": alphabetical groups (untagged last), names
// alphabetical within each group. "manual": the stored array order as-is.
function renderProfiles() {
  const box = $("profiles");
  const empty = $("empty");
  box.innerHTML = "";
  empty.hidden = state.profiles.length > 0;

  if (state.sortMode === "manual") {
    for (const p of state.profiles) box.appendChild(profileCard(p, true));
    return;
  }

  const groups = new Map();
  for (const p of state.profiles) {
    const key = p.tag || "";
    if (!groups.has(key)) groups.set(key, []);
    groups.get(key).push(p);
  }
  const tagged = [...groups.keys()].filter((k) => k !== "").sort((a, b) => a.localeCompare(b));
  const order = groups.has("") ? [...tagged, ""] : tagged;

  for (const key of order) {
    const list = groups.get(key).slice().sort((a, b) => a.name.localeCompare(b.name));
    const header = document.createElement("div");
    header.className = "group-header";
    header.innerHTML = `${key ? escapeHtml(key) : "Senza tag"} <span class="g-count">${list.length}</span>`;
    box.appendChild(header);
    for (const p of list) box.appendChild(profileCard(p, false));
  }
}

function profileCard(p, draggable) {
  const card = document.createElement("div");
  card.className = "card" + (state.selection.has(p.id) ? " selected" : "");
  card.dataset.id = p.id;

  if (draggable) {
    card.draggable = true;
    attachDrag(card);
    const handle = document.createElement("span");
    handle.className = "drag-handle";
    handle.title = "Trascina per riordinare";
    handle.innerHTML = `<svg viewBox="0 0 24 24"><circle cx="9" cy="6" r="1.6"/><circle cx="15" cy="6" r="1.6"/><circle cx="9" cy="12" r="1.6"/><circle cx="15" cy="12" r="1.6"/><circle cx="9" cy="18" r="1.6"/><circle cx="15" cy="18" r="1.6"/></svg>`;
    card.appendChild(handle);
  }

  const dot = document.createElement("span");
  dot.className = "status-dot " + (state.statuses[p.id] || "");
  const status = state.statuses[p.id];
  if (status === "partial") dot.title = "Completata con avvisi: vedi il log";
  if (status === "failed") dot.title = "Fallita: vedi il log";
  if (status === "aborted") dot.title = "Interrotta";

  const body = document.createElement("div");
  body.className = "card-body";

  const opts = [];
  if (p.options.checksum) opts.push("-c");
  if (p.options.delete) opts.push("--delete");
  if (p.options.dryRun) opts.push("-n");
  if (p.options.compress) opts.push("-z");
  if (p.options.verbose) opts.push("-v");
  if (p.options.inplace) opts.push("--inplace");
  const nCustom = (p.options.customExcludes || []).length;
  if (p.options.excludeSystemFiles !== false || nCustom > 0) {
    opts.push(nCustom > 0 ? `--exclude ×${nCustom + (p.options.excludeSystemFiles !== false ? 1 : 0)}` : "--exclude");
  }

  const extraSrc = p.sources.length > 1 ? ` <span class="card-count">+${p.sources.length - 1} sorg.</span>` : "";
  const extraDest = p.destinations.length > 1 ? ` <span class="card-count">+${p.destinations.length - 1} dest.</span>` : "";

  body.innerHTML = `
    <div class="card-title">
      <h3>${escapeHtml(p.name)}</h3>
      ${p.tag ? `<span class="card-tag">${escapeHtml(p.tag)}</span>` : ""}
      <span class="card-opts">${opts.map((o) => `<span class="card-opt">${o}</span>`).join("")}</span>
    </div>
    <div class="card-path">&#x202A;${escapeHtml(p.sources[0] || "")}${extraSrc} <span class="arrow">→</span> ${escapeHtml(p.destinations[0] || "")}${extraDest}&#x202C;</div>
  `;

  const actions = document.createElement("div");
  actions.className = "card-actions";

  const btnEdit = document.createElement("button");
  btnEdit.className = "btn ghost small";
  btnEdit.textContent = "Modifica";
  btnEdit.addEventListener("click", (e) => { e.stopPropagation(); openEditor(p); });

  const btnRun = document.createElement("button");
  btnRun.className = "btn primary small";
  btnRun.textContent = "Avvia";
  btnRun.disabled = state.busy;
  btnRun.addEventListener("click", async (e) => {
    e.stopPropagation();
    openLog();
    try { await api().RunOne(p.id); } catch (err) { showBanner(String(err), true); }
  });

  actions.append(btnEdit, btnRun);

  // Only the profile actually running can be stopped, so the button lives on
  // that row, next to its (disabled) Avvia.
  if (status === "running") {
    const btnAbort = document.createElement("button");
    btnAbort.className = "btn danger small";
    btnAbort.textContent = "Interrompi";
    btnAbort.title = "Ferma la copia come Ctrl+C e annulla quelle ancora in coda";
    btnAbort.addEventListener("click", async (e) => {
      e.stopPropagation();
      try { await api().Abort(); } catch (err) { showBanner(String(err), true); }
    });
    actions.append(btnAbort);
  }

  card.append(dot, body, actions);

  card.addEventListener("click", () => {
    if (state.selection.has(p.id)) state.selection.delete(p.id);
    else state.selection.add(p.id);
    render();
  });

  return card;
}

// ---- drag & drop (manual mode only) -----------------------------------------

let dragId = null;

function attachDrag(card) {
  card.addEventListener("dragstart", (e) => {
    dragId = card.dataset.id;
    card.classList.add("dragging");
    e.dataTransfer.effectAllowed = "move";
  });
  card.addEventListener("dragend", () => {
    dragId = null;
    card.classList.remove("dragging");
    for (const c of $("profiles").querySelectorAll(".card")) {
      c.classList.remove("drop-before", "drop-after");
    }
  });
  card.addEventListener("dragover", (e) => {
    e.preventDefault();
    if (!dragId || card.dataset.id === dragId) return;
    const rect = card.getBoundingClientRect();
    const after = e.clientY > rect.top + rect.height / 2;
    card.classList.toggle("drop-before", !after);
    card.classList.toggle("drop-after", after);
  });
  card.addEventListener("dragleave", () => {
    card.classList.remove("drop-before", "drop-after");
  });
  card.addEventListener("drop", async (e) => {
    e.preventDefault();
    const targetId = card.dataset.id;
    if (!dragId || targetId === dragId) return;
    const rect = card.getBoundingClientRect();
    const after = e.clientY > rect.top + rect.height / 2;
    reorder(dragId, targetId, after);
  });
}

async function reorder(sourceId, targetId, after) {
  const arr = state.profiles.slice();
  const from = arr.findIndex((p) => p.id === sourceId);
  if (from === -1) return;
  const [moved] = arr.splice(from, 1);
  let to = arr.findIndex((p) => p.id === targetId);
  if (to === -1) return;
  if (after) to += 1;
  arr.splice(to, 0, moved);
  state.profiles = arr;
  render();
  try {
    setProfiles(await api().ReorderProfiles(arr.map((p) => p.id)));
    render();
  } catch (e) {
    showBanner(String(e), true);
  }
}

// ---- editor modal ------------------------------------------------------------

function pathRow(listEl, value, browseTitle) {
  const row = document.createElement("div");
  row.className = "path-row";

  const input = document.createElement("input");
  input.type = "text";
  input.value = value || "";
  input.placeholder = "/percorso";

  const browse = document.createElement("button");
  browse.className = "btn ghost small";
  browse.textContent = "Sfoglia…";
  browse.addEventListener("click", async (e) => {
    e.preventDefault();
    const dir = await api().ChooseDirectory(browseTitle);
    if (dir) input.value = dir;
  });

  const remove = document.createElement("button");
  remove.className = "btn ghost small remove-path";
  remove.textContent = "×";
  remove.title = "Rimuovi questo percorso";
  remove.addEventListener("click", (e) => {
    e.preventDefault();
    if (listEl.children.length > 1) row.remove();
    else input.value = "";
  });

  row.append(input, browse, remove);
  listEl.appendChild(row);
}

function listValues(listEl) {
  return [...listEl.querySelectorAll("input")]
    .map((i) => i.value.trim())
    .filter((v) => v !== "");
}

function openEditor(profile) {
  state.editingId = profile ? profile.id : null;
  $("modal-title").textContent = profile ? "Modifica destinazione" : "Nuova destinazione";
  $("f-name").value = profile?.name || "";
  $("f-tag").value = profile?.tag || "";
  $("f-checksum").checked = !!profile?.options.checksum;
  $("f-delete").checked = !!profile?.options.delete;
  $("f-dryrun").checked = !!profile?.options.dryRun;
  $("f-compress").checked = !!profile?.options.compress;
  $("f-verbose").checked = !!profile?.options.verbose;
  $("f-inplace").checked = !!profile?.options.inplace;
  // On for new profiles, and for old ones saved before the option existed
  // (the backend defaults the missing key to true as well).
  $("f-sysexcl").checked = profile ? profile.options.excludeSystemFiles !== false : true;
  $("f-excludes").value = (profile?.options.customExcludes || []).join(", ");

  const srcList = $("src-list");
  const destList = $("dest-list");
  srcList.innerHTML = "";
  destList.innerHTML = "";
  for (const s of (profile?.sources?.length ? profile.sources : [""])) {
    pathRow(srcList, s, "Scegli la cartella sorgente");
  }
  for (const d of (profile?.destinations?.length ? profile.destinations : [""])) {
    pathRow(destList, d, "Scegli la cartella di destinazione");
  }

  $("tag-suggestions").hidden = true;

  $("modal").hidden = false;
  $("f-name").focus();
}

// Native <datalist> only suggests while typing in the macOS webview, so the
// tag field gets a real dropdown: clicking it lists every existing tag.
function setupTagAutocomplete() {
  const input = $("f-tag");
  const box = $("tag-suggestions");

  const showSuggestions = () => {
    const query = input.value.trim().toLowerCase();
    const matches = tags().filter((t) => t.toLowerCase().includes(query));
    box.innerHTML = "";

    if (matches.length === 0) {
      box.hidden = true;
      return;
    }

    for (const tag of matches) {
      const count = state.profiles.filter((p) => p.tag === tag).length;
      const item = document.createElement("button");
      item.type = "button";
      item.innerHTML = `<span class="dot"></span><span>${escapeHtml(tag)}</span><span class="count">${count}</span>`;
      // mousedown fires before the input's blur, so the pick isn't lost when
      // the field loses focus
      item.addEventListener("mousedown", (e) => {
        e.preventDefault();
        input.value = tag;
        box.hidden = true;
      });
      box.appendChild(item);
    }
    box.hidden = false;
  };

  input.addEventListener("focus", showSuggestions);
  input.addEventListener("click", showSuggestions);
  input.addEventListener("input", showSuggestions);
  input.addEventListener("blur", () => { box.hidden = true; });
  input.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !box.hidden) {
      // close only the dropdown; the modal's Escape handler must not fire
      e.stopPropagation();
      box.hidden = true;
    }
  });
}

function closeEditor() {
  $("modal").hidden = true;
  state.editingId = null;
}

async function saveEditor() {
  const profile = {
    id: state.editingId || "",
    name: $("f-name").value,
    sources: listValues($("src-list")),
    destinations: listValues($("dest-list")),
    tag: $("f-tag").value,
    options: {
      checksum: $("f-checksum").checked,
      delete: $("f-delete").checked,
      dryRun: $("f-dryrun").checked,
      compress: $("f-compress").checked,
      verbose: $("f-verbose").checked,
      inplace: $("f-inplace").checked,
      excludeSystemFiles: $("f-sysexcl").checked,
      customExcludes: $("f-excludes").value.split(",").map((s) => s.trim()).filter(Boolean),
    },
  };
  try {
    setProfiles(await api().SaveProfile(profile));
    closeEditor();
    render();
  } catch (e) {
    showBanner(String(e), true);
  }
}

// ---- log side panel ----------------------------------------------------------

// The panel lives to the right of the main content; showing it widens the
// native window (Go side) so it grows outward instead of covering the UI.
async function openLog() {
  if (state.logOpen) return;
  state.logOpen = true;
  $("log-panel").hidden = false;
  try { await api().SetLogVisible(true); } catch (_) {}
}

async function closeLog() {
  if (!state.logOpen) return;
  state.logOpen = false;
  $("log-panel").hidden = true;
  try { await api().SetLogVisible(false); } catch (_) {}
}

function toggleLog() { state.logOpen ? closeLog() : openLog(); }

function appendLog(text) {
  const body = $("log-body");
  body.textContent += text;
  body.scrollTop = body.scrollHeight;
}

// ---- wiring -------------------------------------------------------------------

function wire() {
  setupTagAutocomplete();
  $("btn-add").addEventListener("click", () => openEditor(null));

  $("btn-remove").addEventListener("click", async () => {
    if (state.selection.size === 0) return;
    try {
      setProfiles(await api().DeleteProfiles([...state.selection]));
      state.selection.clear();
      render();
    } catch (e) { showBanner(String(e), true); }
  });

  $("sort-toggle").addEventListener("click", (e) => {
    const btn = e.target.closest("button");
    if (!btn) return;
    state.sortMode = btn.dataset.mode;
    localStorage.setItem("sortMode", state.sortMode);
    render();
  });

  $("btn-cancel").addEventListener("click", closeEditor);
  $("btn-save").addEventListener("click", saveEditor);
  $("modal").addEventListener("click", (e) => { if (e.target === $("modal")) closeEditor(); });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !$("modal").hidden) closeEditor();
  });

  $("btn-add-src").addEventListener("click", (e) => {
    e.preventDefault();
    pathRow($("src-list"), "", "Scegli la cartella sorgente");
  });
  $("btn-add-dest").addEventListener("click", (e) => {
    e.preventDefault();
    pathRow($("dest-list"), "", "Scegli la cartella di destinazione");
  });

  $("btn-log").addEventListener("click", toggleLog);
  $("btn-close-log").addEventListener("click", closeLog);
  $("btn-clear-log").addEventListener("click", () => { $("log-body").textContent = ""; });

  $("btn-verify").addEventListener("click", async () => {
    const dir = await api().ChooseDirectory("Scegli la cartella da verificare");
    if (!dir) return;
    openLog();
    try { await api().VerifyFolder(dir); } catch (e) { showBanner(String(e), true); }
  });

  $("btn-export").addEventListener("click", async () => {
    try {
      const path = await api().ExportProfiles();
      if (path) showBanner("Configurazioni esportate in " + path, false);
    } catch (e) { showBanner(String(e), true); }
  });

  $("btn-import").addEventListener("click", async () => {
    try {
      const count = await api().ImportProfiles();
      if (count >= 0) {
        const s = await api().GetState();
        setProfiles(s.profiles);
        render();
        showBanner("Importate " + count + " destinazioni.", false);
      }
    } catch (e) { showBanner(String(e), true); }
  });

  window.runtime.EventsOn("run:log", appendLog);
  window.runtime.EventsOn("run:busy", (busy) => {
    state.busy = busy;
    render();
  });
  window.runtime.EventsOn("run:status", (ev) => {
    state.statuses[ev.id] = ev.status;
    render();
  });
}

async function init() {
  wire();
  const s = await api().GetState();
  setProfiles(s.profiles);
  state.busy = s.busy;
  const cp = $("config-path");
  cp.textContent = s.configPath;
  cp.title = s.configPath;
  if (!s.rsyncPath) {
    showBanner("rsync non trovato nel PATH: installalo per poter avviare le sincronizzazioni.", true);
  }
  render();
}

window.addEventListener("DOMContentLoaded", init);

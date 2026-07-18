"use strict";

const api = () => window.go.main.App;

const state = {
  profiles: [],
  selection: new Set(),
  statuses: {}, // id -> "running" | "success" | "failed" | "partial"
  windowBusy: false, // verifica o esecuzione in-finestra (eventi run:busy)
  jobsBusy: false,   // job staccati vivi (derivato dal disco)
  editingId: null,
  sortMode: localStorage.getItem("sortMode") || "tag", // "tag" | "manual"
  logOpen: false,
  jobs: [],
  jobOfProfile: {},
  eventStatuses: {}, // dagli eventi run:status (solo modalità in-finestra)
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

// askConfirm shows the shared confirmation dialog and resolves to null when the
// user backs out, or to { checked } when they go ahead — checked being the
// state of the optional extra switch (used for --delete on a restore).
//
// Every caller is about to do something that can destroy data, so the dialog
// always spells out the actual paths involved instead of a generic "are you
// sure": the mistake to prevent is restoring the right way onto the wrong
// folder, and only the paths reveal that.
function askConfirm({ title, bodyHtml, confirmLabel, checkboxLabel }) {
  return new Promise((resolve) => {
    const overlay = $("confirm");
    const check = $("confirm-check");
    const checkInput = $("confirm-check-input");

    $("confirm-title").textContent = title;
    $("confirm-body").innerHTML = bodyHtml;
    $("confirm-ok").textContent = confirmLabel;

    checkInput.checked = false;
    check.hidden = !checkboxLabel;
    if (checkboxLabel) $("confirm-check-label").textContent = checkboxLabel;

    const done = (result) => {
      overlay.hidden = true;
      $("confirm-ok").removeEventListener("click", onOk);
      $("confirm-cancel").removeEventListener("click", onCancel);
      overlay.removeEventListener("click", onBackdrop);
      // must match the capture flag used when adding, or it is not removed
      document.removeEventListener("keydown", onKey, true);
      resolve(result);
    };
    const onOk = () => done({ checked: checkInput.checked });
    const onCancel = () => done(null);
    const onBackdrop = (e) => { if (e.target === overlay) done(null); };
    const onKey = (e) => {
      if (e.key !== "Escape") return;
      // This dialog can sit on top of the editor: stop the editor's own
      // Escape handler from closing that too.
      e.stopPropagation();
      done(null);
    };

    $("confirm-ok").addEventListener("click", onOk);
    $("confirm-cancel").addEventListener("click", onCancel);
    overlay.addEventListener("click", onBackdrop);
    // capture phase, so it runs before the editor's keydown listener
    document.addEventListener("keydown", onKey, true);

    overlay.hidden = false;
    $("confirm-cancel").focus();
  });
}

// ---- rendering --------------------------------------------------------------

function isBusy() { return state.windowBusy || state.jobsBusy; }

function render() {
  renderChips();
  renderProfiles();
  $("btn-remove").disabled = state.selection.size === 0;
  $("btn-verify").disabled = isBusy();
  const st = $("status-text");
  st.textContent = isBusy() ? "Sincronizzazione in corso…" : "Pronto";
  st.classList.toggle("busy", isBusy());
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
  play.disabled = isBusy();
  play.innerHTML = `<svg viewBox="0 0 12 12"><path d="M3 1.5l7 4.5-7 4.5z"/></svg>`;
  play.addEventListener("click", async () => {
    // No openLog here: with detached jobs the output goes to the job file,
    // and an empty panel that fills only after "Segui" just looks broken.
    try { await runFn(); refreshJobs(); } catch (e) { showBanner(String(e), true); }
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

  const status = state.statuses[p.id] || "";
  const job = state.jobOfProfile ? state.jobOfProfile[p.id] : null;
  const dot = document.createElement("span");
  dot.className = "status-dot " + status;
  dot.title = dotTitle(status, state.jobOfProfile ? state.jobOfProfile[p.id] : null);

  const body = document.createElement("div");
  body.className = "card-body";

  const opts = [];
  if (p.options.checksum) opts.push("-c");
  if (p.options.delete) opts.push("--delete");
  if (p.options.dryRun) opts.push("-n");
  if (p.options.compress) opts.push("-z");
  if (p.options.verbose) opts.push("-v");
  if (p.options.inplace) opts.push("--inplace");
  if (p.options.recreateStructure) opts.push("struttura");
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

  if (status === "running" && job) {
    const prog = document.createElement("div");
    prog.className = "card-progress";
    prog.innerHTML = progressBar(job);
    body.appendChild(prog);
  }

  const actions = document.createElement("div");
  actions.className = "card-actions";

  const btnEdit = document.createElement("button");
  btnEdit.className = "btn ghost small";
  btnEdit.textContent = "Modifica";
  btnEdit.addEventListener("click", (e) => { e.stopPropagation(); openEditor(p); });

  const btnRun = document.createElement("button");
  btnRun.className = "btn primary small";
  btnRun.textContent = "Avvia";
  btnRun.disabled = isBusy();
  btnRun.addEventListener("click", async (e) => {
    e.stopPropagation();
    try { await api().RunOne(p.id); refreshJobs(); } catch (err) { showBanner(String(err), true); }
  });

  actions.append(btnEdit);

  // Restore is only meaningful when the reverse copy is unambiguous, which is
  // exactly the one-source/one-destination case (see restorePlan in app.go).
  // On any other profile the button would have to guess where each file came
  // from, so it isn't offered at all rather than offered and refused.
  if (p.sources.length === 1 && p.destinations.length === 1) {
    const btnRestore = document.createElement("button");
    btnRestore.className = "btn ghost small";
    btnRestore.textContent = "Ripristina";
    btnRestore.title = "Copia al contrario: dal backup verso la cartella originale";
    btnRestore.disabled = isBusy();
    btnRestore.addEventListener("click", (e) => {
      e.stopPropagation();
      onRestore(p);
    });
    actions.append(btnRestore);
  }

  actions.append(btnRun);

  // The log of the last run, straight from the card: it is where anyone looks
  // first after a copy finishes, and having to go through Attività for it was
  // a step too many.
  if (job && !job.alive && job.hasLog) {
    const btnLog = document.createElement("button");
    btnLog.className = "btn ghost small";
    btnLog.textContent = "Log";
    btnLog.title = "Mostra il log dell'ultima copia";
    btnLog.addEventListener("click", (e) => { e.stopPropagation(); followJob(job.jobId); });
    actions.append(btnLog);
  }

  // Only the profile actually running can be stopped, so the button lives on
  // that row, next to its (disabled) Avvia.
  if (status === "running") {
    const btnAbort = document.createElement("button");
    btnAbort.className = "btn danger small";
    btnAbort.textContent = "Interrompi";
    btnAbort.title = "Ferma la copia come Ctrl+C e annulla quelle ancora in coda";
    btnAbort.addEventListener("click", async (e) => {
      e.stopPropagation();
      try {
        // A detached job is stopped by signalling its supervisor; Abort only
        // reaches a run happening inside this window.
        if (job && job.alive) await api().StopJob(job.jobId);
        else await api().Abort();
      } catch (err) { showBanner(String(err), true); }
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

// ---- restore -----------------------------------------------------------------

// onRestore runs the copy backwards: from the backup onto the originals. The
// exact paths come from the backend, which knows whether the data sits in the
// destination root or one level down in <dest>/<nome> (ricrea struttura).
async function onRestore(p) {
  let plan;
  try {
    plan = await api().RestorePlanFor(p.id);
  } catch (e) {
    showBanner(String(e), true);
    return;
  }
  if (!plan.allowed) {
    showBanner(plan.reason, true);
    return;
  }

  const answer = await askConfirm({
    title: "Ripristinare dal backup?",
    bodyHtml:
      `La copia viene eseguita <strong>al contrario</strong>: i file del backup vengono riportati ` +
      `nella cartella originale, sovrascrivendo le versioni che si trovano lì.` +
      `<span class="path">Dal backup: ${escapeHtml(plan.from)}</span>` +
      `<span class="path">Alla cartella originale: ${escapeHtml(plan.to)}</span>`,
    confirmLabel: "Ripristina",
    checkboxLabel: "Elimina dall'originale i file che il backup non contiene (--delete)",
  });
  if (!answer) return;

  // --delete going this way is the genuinely destructive combination: it wipes
  // from the user's own folder everything created since the backup was made.
  // One click is not enough consent for that.
  if (answer.checked) {
    const second = await askConfirm({
      title: "Confermi l'eliminazione durante il ripristino?",
      bodyHtml:
        `Ogni file presente in <strong>${escapeHtml(plan.to)}</strong> che non esiste nel backup ` +
        `verrà <strong>eliminato</strong>, compreso tutto ciò che hai creato dopo l'ultima copia.` +
        `<span class="warn">Se il backup non è aggiornatissimo, questa operazione fa perdere dati. ` +
        `Nel dubbio, annulla e ripristina senza eliminazione.</span>`,
      confirmLabel: "Elimina e ripristina",
    });
    if (!second) return;
  }

  try {
    await api().RunRestore(p.id, answer.checked);
    refreshJobs();
  } catch (e) {
    showBanner(String(e), true);
  }
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
    if (!dir) return;
    input.value = dir;
    // Setting .value from script fires no event, and the "ricrea struttura"
    // example quotes these paths — tell the listeners explicitly.
    input.dispatchEvent(new Event("input", { bubbles: true }));
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

// ---- ricrea struttura --------------------------------------------------------

let recreateWasOnAtOpen = false;

// lastSegment mirrors pathBase in app.go: the folder name rsync would recreate.
function lastSegment(path) {
  const s = String(path).replace(/\/+$/, "");
  const slash = s.lastIndexOf("/");
  if (slash >= 0) return s.slice(slash + 1);
  const colon = s.lastIndexOf(":");
  return colon >= 0 ? s.slice(colon + 1) : s;
}

// syncRecreateRow repaints the row (grey/red) and shows, with the profile's own
// paths, where the files will actually land. The abstract description of the
// option is easy to misread; the concrete example is not.
function syncRecreateRow() {
  const on = $("f-recreate").checked;
  $("recreate-row").classList.toggle("on", on);

  const src = listValues($("src-list"))[0] || "";
  const dest = listValues($("dest-list"))[0] || "";
  const example = $("recreate-example");

  if (!src || !dest) {
    example.textContent = on
      ? "cartella di origine ricreata nella destinazione"
      : "contenuto riversato direttamente nella destinazione";
    return;
  }

  const name = lastSegment(src) || "cartella";
  const cleanDest = dest.replace(/\/+$/, "");
  example.textContent = on
    ? `${name} → ${cleanDest}/${name}/…`
    : `contenuto di ${name} → ${cleanDest}/…`;
}

// confirmRecreateOff explains what switching the option back off does to a
// profile that has already run with it on. With --delete the previously copied
// <dest>/<nome> becomes "extra" for rsync and gets removed — a backup deleted
// by a checkbox. Without --delete nothing is deleted, but the destination ends
// up holding both layouts, which is its own mess.
async function confirmRecreateOff() {
  const src = listValues($("src-list"))[0] || "";
  const dest = listValues($("dest-list"))[0] || "";
  const name = lastSegment(src) || "cartella";
  const cleanDest = (dest || "destinazione").replace(/\/+$/, "");
  const withDelete = $("f-delete").checked;

  const consequence = withDelete
    ? `<span class="warn">Il profilo ha <strong>--delete</strong> attivo: alla prossima copia ` +
      `<strong>${escapeHtml(cleanDest)}/${escapeHtml(name)}</strong> risulterà un file di troppo ` +
      `e verrà <strong>eliminato</strong>. Il backup già presente andrebbe perso.</span>`
    : `<span class="warn">Alla prossima copia il contenuto verrà riversato direttamente in ` +
      `<strong>${escapeHtml(cleanDest)}</strong>, lasciando anche la vecchia cartella ` +
      `<strong>${escapeHtml(name)}</strong>: la destinazione conterrà due copie con strutture diverse.</span>`;

  const answer = await askConfirm({
    title: "Disattivare «Ricrea struttura»?",
    bodyHtml:
      `Questo profilo copia già ricreando la cartella dentro la destinazione. ` +
      `Spegnere l'opzione ora cambia il punto in cui i file vengono scritti.` +
      consequence,
    confirmLabel: "Disattiva comunque",
  });
  return answer !== null;
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
  $("f-recreate").checked = !!profile?.options.recreateStructure;
  // Remembered so the warning only fires when switching off something that has
  // already been used for real copies, not on a brand new profile.
  recreateWasOnAtOpen = !!profile?.options.recreateStructure;
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

  // After the path rows exist: the example quotes them, and reading them any
  // earlier would only ever find the lists empty.
  syncRecreateRow();

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
      recreateStructure: $("f-recreate").checked,
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

// ---- log ---------------------------------------------------------------------
//
// A large transfer with -v produces thousands of lines per second. The naive
// version of this (body.textContent += text, then read scrollHeight) was
// measured at ~1700x slower than batching: appending to textContent re-reads
// and rebuilds the whole buffer every time, so the cost grows with the size of
// the log, and reading scrollHeight forces a synchronous layout on each line.
// Together they froze the window for the length of the copy.
//
// Now text is accumulated and written to the DOM once per animation frame, so
// the cost is bounded by the frame rate instead of by rsync's output rate.

const LOG_MAX_CHARS = 400_000; // roughly 5000 lines kept on screen
const LOG_KEEP_CHARS = 300_000; // how much survives a trim
const SCROLL_STICK_PX = 40; // "close enough to the bottom" to keep following

let logPending = "";
let logFrame = null;
let logChars = 0;

function appendLog(text) {
  logPending += text;
  if (logFrame === null) logFrame = requestAnimationFrame(flushLogToDom);
}

function flushLogToDom() {
  logFrame = null;
  if (logPending === "") return;

  const body = $("log-body");
  // Only follow the output if the user is already at the bottom: yanking the
  // view down while they are reading further up is worse than not scrolling.
  const stick = body.scrollHeight - body.scrollTop - body.clientHeight < SCROLL_STICK_PX;

  // A text node is appended, never re-read: what is already on screen is left
  // untouched, which is what keeps this O(new text) instead of O(whole log).
  body.appendChild(document.createTextNode(logPending));
  logChars += logPending.length;
  logPending = "";

  if (logChars > LOG_MAX_CHARS) trimLog(body);
  if (stick) body.scrollTop = body.scrollHeight;
}

// trimLog drops the oldest output once the log gets long. Without a cap a
// multi-hundred-thousand-line transfer would keep growing the DOM until the
// window bogs down for a different reason than the one just fixed.
function trimLog(body) {
  const kept = body.textContent.slice(-LOG_KEEP_CHARS);
  body.textContent = "⋯ righe precedenti troncate ⋯\n" + kept;
  logChars = body.textContent.length;
}

function clearLog() {
  logPending = "";
  logChars = 0;
  if (logFrame !== null) {
    cancelAnimationFrame(logFrame);
    logFrame = null;
  }
  $("log-body").textContent = "";
}


// ---- attività: job staccati ---------------------------------------------------
//
// Da questa versione una copia non appartiene più alla finestra che l'ha
// avviata: prosegue a finestra chiusa e va ritrovata alla riapertura. Tutto
// qui dentro parte quindi dal disco, non da uno stato tenuto in memoria.

// Polling is only frequent when there is something to watch. Asking the
// backend once a second while nothing is running costs a directory scan and a
// lock check per job, for no news.
const JOBS_POLL_ACTIVE_MS = 1000;
const JOBS_POLL_IDLE_MS = 5000;

let jobsTimer = null;
let followedJob = null;    // jobId di cui stiamo mostrando il log
let followedOffset = 0;    // quanto ne abbiamo già letto
let followedSkipping = false; // già avvisato che stiamo saltando

// deriveProfileStatuses rebuilds the per-profile state shown on the cards.
//
// Until 2.2 this came from run:status events emitted while the runner worked
// inside this window. With detached jobs there is nothing to emit — the copy
// may well have been started by a window that no longer exists — so the state
// is read back from the jobs on disk. The most recent job that mentions a
// profile is the one that describes it, which is also why a status now
// survives closing and reopening the app.
function deriveProfileStatuses() {
  const statuses = {};
  const jobOf = {};
  // state.jobs is newest first, so the first mention of a profile wins.
  for (const j of state.jobs) {
    for (const pid of j.profileIds || []) {
      if (statuses[pid] !== undefined) continue;
      statuses[pid] = j.alive ? "running" : j.status;
      jobOf[pid] = j;
    }
  }
  // Event statuses (detach off) fill the gaps the jobs on disk do not cover;
  // they must not be clobbered by the poll, which knows nothing about them.
  for (const [id, st] of Object.entries(state.eventStatuses)) {
    if (statuses[id] === undefined) statuses[id] = st;
  }
  state.statuses = statuses;
  state.jobOfProfile = jobOf;
  // Avvia must be disabled while anything is running, as it always was: with
  // one job at a time, a live job means the app is busy.
  const busy = state.jobs.some((j) => j.alive);
  if (busy !== state.jobsBusy) {
    state.jobsBusy = busy;
    render();
  } else {
    renderProfileDots();
    renderProfileProgress();
  }
}

// renderProfileDots repaints just the dots, so the poll does not rebuild the
// whole list once a second and lose scroll position or selection.
function renderProfileDots() {
  for (const card of $("profiles").querySelectorAll(".card")) {
    const id = card.dataset.id;
    const dot = card.querySelector(".status-dot");
    if (!dot) continue;
    const st = state.statuses[id] || "";
    dot.className = "status-dot " + st;
    dot.title = dotTitle(st, state.jobOfProfile ? state.jobOfProfile[id] : null);
  }
}

// renderProfileProgress refreshes just the bars, so the poll does not rebuild
// the whole list once a second.
function renderProfileProgress() {
  for (const card of $("profiles").querySelectorAll(".card")) {
    const holder = card.querySelector(".card-progress");
    if (!holder) continue;
    const job = state.jobOfProfile ? state.jobOfProfile[card.dataset.id] : null;
    if (job) holder.innerHTML = progressBar(job);
  }
}

function dotTitle(status, job) {
  const summary = job && job.summary ? job.summary : "";
  // The summary already tells the whole story for an interrupted job; adding a
  // label in front of it would just say the same thing twice.
  if (status === "orphaned") return summary || "Interrotta in modo anomalo: esito non noto";

  const extra = summary ? " — " + summary : "";
  switch (status) {
    case "running": return "Copia in corso" + extra;
    case "success": return "Ultima copia completata" + extra;
    case "partial": return "Completata con avvisi: vedi il log" + extra;
    case "failed":  return "Fallita: vedi il log" + extra;
    case "aborted": return "Interrotta" + extra;
    default:        return "";
  }
}


// progressBar renders the bar. A percentage below zero means rsync has not
// said anything usable yet — an incremental run with nothing to copy never
// reports at all — so the bar shuttles instead of claiming to be at zero.
function progressBar(job) {
  const known = job.percent >= 0;
  const files = job.filesTotal > 0 ? `${job.filesDone}/${job.filesTotal} file` : "";
  const label = known ? `${job.percent}%` : "in corso";
  return `<span class="progress${known ? "" : " indeterminate"}">
      <span class="progress-fill" style="${known ? `width:${job.percent}%` : ""}"></span>
    </span>
    <span class="progress-label">${label}${files ? " · " + escapeHtml(files) : ""}</span>`;
}

function jobStatusLabel(j) {
  if (j.alive) return "in corso";
  switch (j.status) {
    case "success":  return "completata";
    case "partial":  return "completata con avvisi";
    case "failed":   return "fallita";
    case "aborted":  return "interrotta";
    case "orphaned": return "interrotta in modo anomalo";
    default:         return j.status;
  }
}

function shortTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : d.toLocaleString();
}

async function refreshJobs() {
  let list;
  try { list = await api().ListJobs(); } catch (_) { return; }
  state.jobs = list || [];
  deriveProfileStatuses();

  const live = state.jobs.filter((j) => j.alive);
  const badge = $("jobs-badge");
  badge.hidden = live.length === 0;
  badge.textContent = String(live.length);

  renderRunningNote(live);
  if (!$("jobs-modal").hidden) renderJobsList();
  if (followedJob) pullFollowedLog();
}

// A job left running by a previous session is the whole point of the feature,
// so the window says so instead of looking idle while a copy is under way.
function renderRunningNote(live) {
  const note = $("running-note");
  const orphans = state.jobs.filter((j) => j.status === "orphaned");

  if (live.length > 0) {
    note.className = "running-note";
    note.innerHTML = live.length === 1
      ? `<span class="pulse"></span><span><strong>${escapeHtml(live[0].label)}</strong>${
          live[0].currentProfile ? " — " + escapeHtml(live[0].currentProfile) : ""}</span>` +
        progressBar(live[0])
      : `<span class="pulse"></span><span>${live.length} copie sono in corso</span>`;
    note.hidden = false;
    return;
  }
  if (orphans.length > 0) {
    note.className = "running-note orphan";
    note.innerHTML = `<span class="pulse"></span><span>${orphans.length === 1
      ? "Una copia si è interrotta in modo anomalo: l'esito non è noto."
      : `${orphans.length} copie si sono interrotte in modo anomalo.`}</span>`;
    note.hidden = false;
    return;
  }
  note.hidden = true;
}

function renderJobsList() {
  const box = $("jobs-list");
  box.innerHTML = "";
  if (state.jobs.length === 0) {
    box.innerHTML = `<div class="jobs-empty">Nessuna copia recente.</div>`;
    return;
  }

  for (const j of state.jobs) {
    const row = document.createElement("div");
    row.className = "job-row";

    const dot = document.createElement("span");
    dot.className = "job-dot " + (j.alive ? "running" : j.status);

    const main = document.createElement("div");
    main.className = "job-main";
    const where = j.alive && j.currentProfile ? ` — ${j.currentProfile}` : "";
    main.innerHTML = `
      <span class="job-name">${escapeHtml(j.label)}${escapeHtml(where)}</span>
      <span class="job-meta">${escapeHtml(jobStatusLabel(j))} · ${escapeHtml(shortTime(j.startedAt))}${
        j.summary ? " · " + escapeHtml(j.summary) : ""}</span>`;

    if (j.alive) {
      const prog = document.createElement("div");
      prog.className = "job-progress";
      prog.innerHTML = progressBar(j);
      main.appendChild(prog);
    }

    const actions = document.createElement("div");
    actions.className = "job-actions";

    // "Segui" and not "Riprendi": this reattaches to a copy already under way.
    // Restarting an interrupted one is what Avvia already does — rsync is
    // incremental, so running it again resumes by its nature.
    if (j.alive || j.hasLog) {
      const follow = document.createElement("button");
      follow.className = "btn ghost small";
      follow.textContent = j.alive ? "Segui" : "Log";
      follow.addEventListener("click", () => followJob(j.jobId));
      actions.append(follow);
    }
    if (j.alive) {
      const stop = document.createElement("button");
      stop.className = "btn danger small";
      stop.textContent = "Interrompi";
      stop.addEventListener("click", async () => {
        try { await api().StopJob(j.jobId); } catch (e) { showBanner(String(e), true); }
      });
      actions.append(stop);
    }

    row.append(dot, main, actions);
    box.appendChild(row);
  }
}

// followJob shows a job's log in the side panel, reading it from disk in
// chunks. Chunks, not lines: a job that ran unattended can have written
// megabytes, and feeding those to the webview one line at a time is exactly
// what froze the window before 2.2.1.
async function followJob(jobId) {
  followedJob = jobId;
  // 0 means "give me the end of it": a job running unattended can have
  // written megabytes, and replaying them would stall the window without
  // showing anything anyone wanted to read.
  followedOffset = 0;
  followedSkipping = false;
  clearLog();
  closeJobsModal();
  openLog();
  await pullFollowedLog();
}

async function pullFollowedLog() {
  if (!followedJob) return;
  try {
    const chunk = await api().ReadJobLog(followedJob, followedOffset);
    if (chunk.missing) {
      if (followedOffset === 0) {
        const j = state.jobs.find((x) => x.jobId === followedJob);
        appendLog((j && j.summary ? j.summary + "\n" : "") +
          "(il log di questa copia è stato cancellato: era andata a buon fine)\n");
      }
      followedJob = null;
      return;
    }
    // A job writing faster than we read makes every poll a skip; saying so
    // each time would bury the output under its own warning. Once is enough,
    // until we manage to keep up again.
    if (chunk.skipped && !followedSkipping) {
      appendLog(followedOffset === 0
        ? "⋯ log lungo: mostrate solo le righe finali ⋯\n"
        : "⋯ il log corre più veloce di quanto si possa mostrare: righe non mostrate ⋯\n");
    }
    followedSkipping = chunk.skipped;
    if (chunk.text) {
      appendLog(chunk.text);
    }
    followedOffset = chunk.offset;
    const j = state.jobs.find((x) => x.jobId === followedJob);
    if (j && !j.alive && !chunk.text) followedJob = null; // finito e letto tutto
  } catch (_) {
    followedJob = null;
  }
}

function openJobsModal() {
  $("jobs-modal").hidden = false;
  renderJobsList();
}

// openRsyncModal shows where to get rsync, hiding the platform rows that make
// no sense where we are.
function openRsyncModal(platform) {
  const isMac = platform === "darwin";
  const isWin = platform === "windows";
  for (const b of document.querySelectorAll("#rsync-modal .mac-only")) b.hidden = !isMac;
  for (const b of document.querySelectorAll("#rsync-modal .win-only")) b.hidden = !isWin;
  $("rsync-modal").hidden = false;
}
function closeJobsModal() { $("jobs-modal").hidden = true; }

function startJobsPolling() {
  if (jobsTimer !== null) return;
  scheduleJobsPoll(0);
}

// A self-rescheduling timeout rather than setInterval: a slow poll must not
// have the next one queued up behind it.
function scheduleJobsPoll(delay) {
  jobsTimer = setTimeout(async () => {
    await refreshJobs();
    const busy = followedJob !== null ||
      state.jobs.some((j) => j.alive) ||
      !$("jobs-modal").hidden;
    scheduleJobsPoll(busy ? JOBS_POLL_ACTIVE_MS : JOBS_POLL_IDLE_MS);
  }, delay);
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

  // The checkbox has already flipped by the time "change" fires, so a refused
  // confirmation puts it back on.
  $("f-recreate").addEventListener("change", async (e) => {
    if (!e.target.checked && recreateWasOnAtOpen) {
      if (!(await confirmRecreateOff())) {
        e.target.checked = true;
      }
    }
    syncRecreateRow();
  });
  // The live example quotes the paths, so it has to follow them as they change.
  $("f-delete").addEventListener("change", syncRecreateRow);
  for (const list of ["src-list", "dest-list"]) {
    $(list).addEventListener("input", syncRecreateRow);
  }

  $("btn-jobs").addEventListener("click", openJobsModal);

  $("btn-clear-history").addEventListener("click", async () => {
    try {
      const n = await api().ClearJobHistory();
      state.eventStatuses = {};
      await refreshJobs();
      render();
      showBanner(n > 0 ? `Rimosse ${n} copie dalla cronologia.` : "Nessuna copia da rimuovere.", false);
    } catch (e) { showBanner(String(e), true); }
  });
  $("f-retention").addEventListener("change", async (e) => {
    const hours = Math.max(1, Math.min(720, parseInt(e.target.value, 10) || 8));
    e.target.value = hours;
    try { await api().SetHistoryRetention(hours); }
    catch (err) { showBanner(String(err), true); }
  });
  $("btn-rsync-info").addEventListener("click", () => openRsyncModal(state.platform));
  $("rsync-close").addEventListener("click", () => { $("rsync-modal").hidden = true; });
  $("rsync-modal").addEventListener("click", (e) => {
    if (e.target === $("rsync-modal")) $("rsync-modal").hidden = true;
  });
  for (const b of document.querySelectorAll("#rsync-modal [data-site]")) {
    b.addEventListener("click", () => {
      api().OpenRsyncSite(b.dataset.site).catch((e) => showBanner(String(e), true));
    });
  }
  $("jobs-close").addEventListener("click", closeJobsModal);
  $("jobs-modal").addEventListener("click", (e) => {
    if (e.target === $("jobs-modal")) closeJobsModal();
  });
  $("f-detach").addEventListener("change", async (e) => {
    try { await api().SetDetachJobs(e.target.checked); }
    catch (err) { showBanner(String(err), true); }
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
  $("btn-clear-log").addEventListener("click", clearLog);

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
    state.windowBusy = busy;
    render();
  });
  window.runtime.EventsOn("jobs:changed", refreshJobs);
  window.runtime.EventsOn("run:status", (ev) => {
    state.eventStatuses[ev.id] = ev.status;
    render();
  });
}

async function init() {
  wire();
  const s = await api().GetState();
  setProfiles(s.profiles);
  state.windowBusy = s.busy;
  const cp = $("config-path");
  cp.textContent = s.configPath;
  cp.title = s.configPath;
  $("f-detach").checked = s.detachJobs !== false;
  $("f-retention").value = s.historyRetentionHours || 8;
  state.platform = navigator.platform.toLowerCase().includes("mac") ? "darwin"
    : navigator.platform.toLowerCase().includes("win") ? "windows" : "linux";
  startJobsPolling();
  if (!s.rsyncPath) {
    // Without rsync this app can do nothing at all: a banner is too easy to
    // miss, so the install guide opens by itself.
    showBanner("rsync non trovato nel PATH: installalo per poter avviare le sincronizzazioni.", true);
    openRsyncModal(state.platform);
  }
  render();
}

window.addEventListener("DOMContentLoaded", init);

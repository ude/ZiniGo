const App = window.go.main.App;
const Events = window.runtime;

const $ = (id) => document.getElementById(id);

let issues = [];

// ---- init ----
window.addEventListener("DOMContentLoaded", async () => {
  const cfg = await App.GetConfig();
  $("username").value = cfg.username || "";
  $("folder-path").textContent = "Descargas en: " + cfg.downloadDir;
  if (cfg.hasPassword) $("password").placeholder = "Contraseña (guardada)";

  $("login-form").addEventListener("submit", onLogin);
  $("folder-btn").addEventListener("click", () => App.OpenDownloadDir());
  $("choose-folder-btn").addEventListener("click", onChooseFolder);
  $("download-all-btn").addEventListener("click", onDownloadAll);

  Events.EventsOn("download:progress", (e) => updateProgress(e));
  Events.EventsOn("download:done", (e) => markDone(e));
  Events.EventsOn("download:error", (e) => markError(e));
});

// ---- login ----
async function onLogin(ev) {
  ev.preventDefault();
  const btn = $("login-btn");
  btn.disabled = true;
  btn.textContent = "Entrando…";
  $("login-error").textContent = "";
  try {
    const email = await App.Login($("username").value, $("password").value, $("remember").checked);
    $("user-email").textContent = email;
    $("login-view").classList.add("hidden");
    $("library-view").classList.remove("hidden");
    await loadLibrary();
  } catch (err) {
    $("login-error").textContent = String(err);
  } finally {
    btn.disabled = false;
    btn.textContent = "Entrar";
  }
}

// ---- library ----
async function loadLibrary() {
  const grid = $("grid");
  grid.innerHTML = "<p class='muted'>Cargando biblioteca…</p>";
  try {
    issues = (await App.ListIssues()) || [];
  } catch (err) {
    grid.innerHTML = `<p class='error'>${err}</p>`;
    return;
  }
  grid.innerHTML = "";
  for (const issue of issues) {
    grid.appendChild(renderCard(issue));
  }
}

function renderCard(issue) {
  const card = document.createElement("div");
  card.className = "card";
  card.id = `issue-${issue.id}`;
  card.innerHTML = `
    <img class="cover" src="${issue.coverUrl}" loading="lazy" alt="" />
    <div class="card-body">
      <div class="card-title">${escapeHtml(issue.name)}</div>
      <div class="card-pub">${escapeHtml(issue.publication)}</div>
      <div class="progress hidden"><div></div></div>
      <div class="progress-label hidden"></div>
      <div class="card-actions"></div>
    </div>`;
  renderActions(card, issue);
  return card;
}

function renderActions(card, issue) {
  const actions = card.querySelector(".card-actions");
  actions.innerHTML = "";
  if (issue.downloaded) {
    const badge = document.createElement("span");
    badge.className = "badge";
    badge.textContent = "✓ Descargada";
    const open = document.createElement("button");
    open.className = "secondary";
    open.textContent = "Abrir";
    open.onclick = () => App.OpenFile(issue.path);
    actions.append(badge, open);
  } else if (issue.downloading) {
    const cancel = document.createElement("button");
    cancel.className = "secondary";
    cancel.textContent = "Cancelar";
    cancel.onclick = () => App.Cancel(issue.id);
    actions.append(cancel);
  } else {
    const dl = document.createElement("button");
    dl.textContent = "Descargar";
    dl.onclick = () => startDownload(issue);
    actions.append(dl);
    if (issue.error) {
      const label = card.querySelector(".progress-label");
      label.classList.remove("hidden");
      label.textContent = "Error: " + issue.error;
    }
  }
}

async function startDownload(issue) {
  try {
    await App.Download(issue.id, issue.name, issue.publication);
    issue.downloading = true;
    issue.error = null;
    const card = $(`issue-${issue.id}`);
    card.querySelector(".progress").classList.remove("hidden");
    const label = card.querySelector(".progress-label");
    label.classList.remove("hidden");
    label.textContent = "Preparando…";
    renderActions(card, issue);
  } catch (err) {
    issue.error = String(err);
  }
}

function onDownloadAll() {
  for (const issue of issues) {
    if (!issue.downloaded && !issue.downloading) startDownload(issue);
  }
}

// ---- progress events ----
function findIssue(id) {
  return issues.find((i) => i.id === id);
}

function updateProgress(e) {
  const card = $(`issue-${e.id}`);
  if (!card) return;
  const pct = e.total ? Math.round((e.done / e.total) * 100) : 0;
  card.querySelector(".progress").classList.remove("hidden");
  card.querySelector(".progress > div").style.width = pct + "%";
  const label = card.querySelector(".progress-label");
  label.classList.remove("hidden");
  label.textContent = `${e.done}/${e.total} páginas`;
}

function markDone(e) {
  const issue = findIssue(e.id);
  const card = $(`issue-${e.id}`);
  if (!issue || !card) return;
  issue.downloaded = true;
  issue.downloading = false;
  issue.path = e.path;
  card.querySelector(".progress").classList.add("hidden");
  card.querySelector(".progress-label").classList.add("hidden");
  renderActions(card, issue);
}

function markError(e) {
  const issue = findIssue(e.id);
  const card = $(`issue-${e.id}`);
  if (!issue || !card) return;
  issue.downloading = false;
  issue.error = e.error;
  card.querySelector(".progress").classList.add("hidden");
  renderActions(card, issue);
}

async function onChooseFolder() {
  const dir = await App.ChooseDownloadDir();
  $("folder-path").textContent = "Descargas en: " + dir;
  await loadLibrary();
}

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  })[c]);
}

const $ = (id) => document.getElementById(id);

const state = {
  items: [],
  total: 0,
  page: 1,
  limit: 25,
};

function fmtTime(secs) {
  secs = Math.max(0, Number(secs) || 0);
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = Math.floor(secs % 60);
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

function fmtHours(h) {
  if (!h) return "0h";
  if (h < 1) return `${Math.round(h * 60)}m`;
  return `${h.toFixed(1)}h`;
}

function fmtWhen(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
}

function titleOf(media) {
  if (!media) return "Unknown";
  if (media.kind === "episode" && media.show_title) {
    const se = media.season != null && media.episode != null
      ? ` S${String(media.season).padStart(2, "0")}E${String(media.episode).padStart(2, "0")}`
      : "";
    return media.title ? `${media.show_title}${se} – ${media.title}` : `${media.show_title}${se}`;
  }
  if (media.kind === "iptv" && media.show_title && media.title && media.show_title !== media.title) {
    return `${media.show_title} – ${media.title}`;
  }
  if (media.kind === "movie" && media.year) return `${media.title} (${media.year})`;
  return media.title || "Untitled";
}

function nowMetaLine(media) {
  const kind = media.kind || "video";
  const url = media.source_url || "";
  if (kind === "youtube") return "youtube";
  if (kind === "iptv") return media.show_title && media.show_title !== media.title ? "live tv" : "iptv";
  if (/^https?:\/\//i.test(url)) {
    try { return `${kind} · ${new URL(url).host}`; } catch (_) { return kind; }
  }
  return kind;
}

function isHttpUrl(s) {
  return /^https?:\/\//i.test(s || "");
}

function posterUrl(media) {
  if (media && media.poster_path) return `/posters/${media.poster_path}`;
  return "";
}

function artWide(kind) {
  return ["youtube", "web", "iptv", "musicvideo", "plugin"].includes(String(kind || "").toLowerCase());
}

function applyArtShape(el, kind, url) {
  const wide = artWide(kind);
  el.classList.toggle("wide", wide);
  if (kind) el.dataset.kind = kind;
  else el.removeAttribute("data-kind");
  if (!url || wide) return;
  const img = new Image();
  img.onload = () => {
    if (img.naturalWidth > img.naturalHeight) el.classList.add("wide");
  };
  img.src = url;
}

function setFindKodiVisible(connected) {
  const btn = $("scanBtn");
  if (btn) btn.hidden = !!connected;
}

function setStatus(mode, text) {
  const el = $("status");
  el.className = "status " + mode;
  $("statusText").textContent = text;
}

function renderNow(now) {
  const card = $("nowCard");
  setFindKodiVisible(now && now.connected);
  if (!now || !now.connected) {
    setStatus("bad", "Kodi offline");
    setFindKodiVisible(false);
  } else if (now.playing) {
    setStatus("live", "Now playing");
  } else if (now.paused) {
    setStatus("ok", "Paused");
  } else {
    setStatus("ok", "Kodi idle");
  }

  if (!now || !now.media) {
    card.classList.add("idle");
    const idleFor = now && now.idle_seconds ? fmtTime(now.idle_seconds) : "0:00";
    $("nowEyebrow").textContent = now && now.connected ? "Box idle" : "Waiting for Kodi";
    $("nowTitle").textContent = now && now.connected ? "Nothing playing" : "Offline";
    $("nowMeta").textContent = now && now.connected
      ? `Kodi is online and sitting idle · ${idleFor}`
      : "Start the app on the same network as the box.";
    $("nowBar").style.width = "0%";
    $("nowPos").textContent = idleFor;
    $("nowDur").textContent = "";
    $("nowState").textContent = now && now.connected ? "idle" : "";
    const idleBox = now && now.connected;
    $("nowArt").className = idleBox ? "now-art zzz" : "now-art empty";
    $("nowArt").style.backgroundImage = "";
    $("nowArt").textContent = idleBox ? "zzz" : "";
    return;
  }
  card.classList.remove("idle");
  const media = now.media;
  applyArtShape(card, media.kind, posterUrl(media));
  $("nowEyebrow").textContent = now.paused ? "Paused" : "Now playing";
  $("nowTitle").textContent = titleOf(media);
  const boxBit = now.box_name || now.box_host;
  $("nowMeta").textContent = [nowMetaLine(media), boxBit ? "on " + boxBit : ""].filter(Boolean).join(" · ");
  const live = media.kind === "iptv" || (!now.runtime_seconds && now.playing);
  $("nowBar").style.width = live ? "100%" : `${Math.min(100, now.progress_percent || 0)}%`;
  $("nowPos").textContent = fmtTime(now.watched_seconds || now.position_seconds);
  $("nowDur").textContent = live ? "LIVE" : fmtTime(now.runtime_seconds);
  const pauseNow = now.paused_seconds ? ` · paused ${fmtTime(now.paused_seconds)}` : "";
  $("nowState").textContent = now.paused
    ? `paused${pauseNow}`
    : (live ? `live · ${fmtTime(now.watched_seconds)}${pauseNow}` : `watched ${fmtTime(now.watched_seconds)}${pauseNow}`);
  const art = $("nowArt");
  art.textContent = "";
  const url = posterUrl(media);
  if (url) {
    art.className = "now-art";
    art.style.backgroundImage = `url('${url}')`;
  } else {
    art.className = "now-art empty";
    art.style.backgroundImage = "";
  }
}

function renderStats(s) {
  if (!s) return;
  $("stats").innerHTML = [
    ["Hours watched", fmtHours(s.total_hours)],
    ["Hours idle", fmtHours(s.idle_hours)],
    ["Watched this week", fmtHours(s.this_week_hours)],
    ["Idle this week", fmtHours(s.this_week_idle_hours)],
    ["Sessions", s.total_watches],
    ["Titles", s.unique_titles],
  ].map(([label, val]) => `<div class="stat"><b>${val}</b><span>${label}</span></div>`).join("");
}

function renderLog(reset) {
  const root = $("log");
  if (reset) root.innerHTML = "";
  if (!state.items.length) {
    root.innerHTML = `<div class="empty-log">No watches yet. Play something on Kodi.</div>`;
    renderPager();
    return;
  }
  const html = state.items.map((w) => {
    const m = w.media || {};
    const art = posterUrl(m);
    const src = isHttpUrl(m.source_url)
      ? `<a class="link" href="${m.source_url}" target="_blank" rel="noopener">open source</a>`
      : "";
    const isIdle = m.kind === "idle";
    const boxLabel = w.box_name || w.box_host || "";
    const isLive = m.kind === "iptv";
    const boxKey = w.box_host || w.box_name || "";
    const boxBit = boxLabel ? ` · ${escapeHtml(boxLabel)}` : "";
    const pauseBit = !isIdle && w.paused_seconds ? ` · paused ${fmtTime(w.paused_seconds)}` : "";
    const subtitle = isIdle
      ? `${fmtWhen(w.started_at)} → ${fmtWhen(w.ended_at)} · nothing playing${boxBit}`
      : isLive
        ? `${fmtWhen(w.started_at)} · live TV${boxBit}${pauseBit}`
        : `${fmtWhen(w.started_at)} · reached ${fmtTime(w.position_seconds)}${w.runtime_seconds ? " / " + fmtTime(w.runtime_seconds) : ""} ${w.completed ? " · finished" : ""}${boxBit}${pauseBit}`;
    return `<article class="card${artWide(m.kind) ? " wide" : ""}" data-kind="${escapeHtml(m.kind || "")}" style="border-left: 4px solid ${boxColor(boxKey || "none")}">
      <div class="poster ${isIdle ? "zzz" : art ? "" : "empty"}" style="${art ? `background-image:url('${art}')` : ""}">${isIdle ? "zzz" : ""}</div>
      <div>
        <h3>${escapeHtml(isIdle ? "Idle" : titleOf(m))}</h3>
        <p>${subtitle}</p>
        <span class="kind">${m.kind || "video"}</span>
        ${src ? `<p style="margin-top:6px">${src}</p>` : ""}
      </div>
      <div class="right">
        <b>${fmtTime(w.watched_seconds)}</b>
        ${isIdle ? "idle" : "watched"}
        ${!isIdle && w.paused_seconds ? `<div class="pause-note">paused ${fmtTime(w.paused_seconds)}</div>` : ""}
      </div>
    </article>`;
  }).join("");
  root.innerHTML = html;
  renderPager();
}

function escapeHtml(s) {
  return String(s || "").replace(/[&<>"']/g, (c) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  }[c]));
}

function ymd(d) {
  const p = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

function boxHue(key) {
  const s = String(key || "");
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 33 + s.charCodeAt(i)) % 360;
  return h;
}

function boxColor(key) {
  return `hsl(${boxHue(key)} 62% 52%)`;
}

function filters() {
  return {
    q: $("search").value.trim(),
    kind: $("kind").value,
    box: $("box") ? $("box").value : "all",
    from: $("from").value ? new Date($("from").value + "T00:00:00").toISOString() : "",
    to: $("to").value ? new Date($("to").value + "T23:59:59").toISOString() : "",
  };
}

function pageCount() {
  return Math.max(1, Math.ceil((state.total || 0) / state.limit));
}

function renderPager() {
  const el = $("pager");
  if (!el) return;
  if ($("view").value === "series") {
    el.hidden = true;
    return;
  }
  const pages = pageCount();
  if (state.total <= state.limit) {
    el.hidden = true;
    return;
  }
  el.hidden = false;
  $("pageLabel").textContent = `Page ${state.page} of ${pages}`;
  $("prevPage").disabled = state.page <= 1;
  $("nextPage").disabled = state.page >= pages;
}

async function loadWatches(resetPage = false) {
  if ($("view").value === "series") {
    const pager = $("pager");
    if (pager) pager.hidden = true;
    await loadSeries();
    return;
  }
  if (resetPage) state.page = 1;
  const f = filters();
  const offset = (state.page - 1) * state.limit;
  const params = new URLSearchParams({
    q: f.q, kind: f.kind, box: f.box || "all", from: f.from, to: f.to,
    limit: String(state.limit), offset: String(offset),
  });
  const res = await fetch("/api/watches?" + params.toString());
  const data = await res.json();
  state.total = data.total || 0;
  const pages = pageCount();
  if (state.page > pages) {
    state.page = pages;
    return loadWatches(false);
  }
  state.items = data.items || [];
  renderLog(true);
}

async function loadSeries() {
  const res = await fetch("/api/series");
  const data = await res.json();
  const q = $("search").value.trim().toLowerCase();
  let items = data.items || [];
  if (q) items = items.filter((s) => (s.show_title || "").toLowerCase().includes(q));
  const root = $("log");
  const pager = $("pager");
  if (pager) pager.hidden = true;
  if (!items.length) {
    root.innerHTML = `<div class="empty-log">No series sessions yet.</div>`;
    return;
  }
  root.innerHTML = items.map((s) => {
    const art = s.poster_path ? `/posters/${s.poster_path}` : "";
    const seasons = (s.seasons || []).map((n) => `S${String(n).padStart(2, "0")}`).join(" ");
    return `<article class="card">
      <div class="poster ${art ? "" : "empty"}" style="${art ? `background-image:url('${art}')` : ""}"></div>
      <div>
        <h3>${escapeHtml(s.show_title)}</h3>
        <p>${s.episodes} episode${s.episodes === 1 ? "" : "s"} · ${s.sessions} session${s.sessions === 1 ? "" : "s"} · ${s.completed} finished${seasons ? " · " + seasons : ""}</p>
        <span class="kind">series</span>
        <p>${fmtWhen(s.last_watched)}</p>
      </div>
      <div class="right">
        <b>${fmtTime(s.watched_seconds)}</b>
        watched
      </div>
    </article>`;
  }).join("");
}

function heatLevel(n, max) {
  if (!n) return "";
  const r = n / Math.max(1, max);
  return r < 0.25 ? "l1" : r < 0.5 ? "l2" : r < 0.75 ? "l3" : "l4";
}

function hourMarks() {
  return [0,3,6,9,12,15,18,21].map((h) => `<span>${String(h).padStart(2,"0")}</span>`).join("");
}

async function loadHeat() {
  const range = document.querySelector("#rangeChips .chip.on")?.dataset.range || "all";
  const res = await fetch("/api/heatmap?range=" + encodeURIComponent(range));
  const data = await res.json();
  const items = data.items || [];
  const mode = data.mode || range;
  const root = $("heat");
  const title = $("heatTitle");
  const y = $("heatY");
  const x = $("heatX");
  const days = ["M","T","W","T","F","S","S"];
  if (y) y.innerHTML = days.map((d) => `<span>${d}</span>`).join("");
  if (x) x.innerHTML = "";
  if (!items.length) {
    root.innerHTML = "";
    root.className = "heat";
    if (title) title.textContent = "Activity";
    return;
  }
  const max = Math.max(1, ...items.map((d) => d.watched_seconds));
  const cells = [];
  let lit = 0;
  const cell = (d, extra) => {
    if (d && d.watched_seconds > 0) lit += 1;
    const tip = d
      ? `${d.label || d.key || d.date}: ${fmtTime(d.watched_seconds)} watched` + (d.idle_seconds ? `, ${fmtTime(d.idle_seconds)} idle` : "")
      : "";
    return `<i class="${d ? heatLevel(d.watched_seconds, max) : ""}" title="${tip}">${extra || ""}</i>`;
  };

  if (mode === "year" || mode === "all") {
    root.className = "heat heat-year";
    if (title) title.textContent = "Year";
    if (y) y.hidden = false;
    const first = new Date((items[0].key || items[0].date) + "T12:00:00");
    const pad = (first.getDay() + 6) % 7;
    for (let i = 0; i < pad; i++) cells.push(cell(null));
    items.forEach((d) => cells.push(cell(d)));
    if (x) {
      const names = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];
      const months = [];
      let last = "";
      items.forEach((d, i) => {
        const key = d.key || d.date || "";
        const m = key.slice(0, 7);
        if (m && m !== last) {
          const week = Math.floor((pad + i) / 7) + 1;
          const mon = names[Math.max(0, Number(key.slice(5, 7)) - 1)] || key.slice(5, 7);
          months.push(`<span style="grid-column:${week}">${mon}</span>`);
          last = m;
        }
      });
      x.className = "heat-x heat-x-year";
      x.innerHTML = months.join("");
    }
  } else if (mode === "today") {
    root.className = "heat heat-hours";
    if (title) title.textContent = "Today by hour";
    if (y) y.hidden = true;
    items.forEach((d) => cells.push(cell(d)));
    if (x) { x.className = "heat-x heat-x-hours"; x.innerHTML = hourMarks(); }
  } else if (mode === "week") {
    root.className = "heat heat-week";
    if (title) title.textContent = "This week by hour";
    if (y) y.hidden = false;
    items.forEach((d) => cells.push(cell(d)));
    if (x) { x.className = "heat-x heat-x-hours"; x.innerHTML = hourMarks(); }
  } else {
    root.className = "heat heat-month";
    if (title) title.textContent = "This month";
    if (y) y.hidden = false;
    const firstKey = items[0].key || items[0].date;
    const first = new Date(firstKey + "T12:00:00");
    const pad = (first.getDay() + 6) % 7;
    for (let i = 0; i < pad; i++) cells.push(cell(null));
    items.forEach((d) => {
      const day = String((d.key || d.date || "").slice(-2)).replace(/^0/, "") || "";
      cells.push(cell(d, day ? `<em>${day}</em>` : ""));
    });
    if (x) { x.className = "heat-x"; x.innerHTML = days.map((d) => `<span>${d}</span>`).join(""); }
  }
  root.innerHTML = cells.join("");
  $("heatLegend").textContent = mode === "today" || mode === "week"
    ? `${lit} hour${lit === 1 ? "" : "s"} with playback`
    : `${lit} day${lit === 1 ? "" : "s"} with playback`;
}

async function refreshNow() {
  const res = await fetch("/api/now");
  renderNow(await res.json());
}

async function refreshStats() {
  const res = await fetch("/api/stats");
  renderStats(await res.json());
}

function connectSSE() {
  const es = new EventSource("/api/events");
  es.onmessage = (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      if (msg.now) renderNow(msg.now);
      if (msg.type === "logged" || msg.type === "poster") {
        loadWatches(true);
        refreshStats();
        loadHeat();
        loadBoxes();
      }
    } catch (_) {}
  };
  es.onerror = () => {
    es.close();
    setTimeout(connectSSE, 3000);
  };
}

["search", "kind", "box", "from", "to", "view"].forEach((id) => {
  $(id).addEventListener("input", debounce(() => loadWatches(true), 200));
  $(id).addEventListener("change", () => loadWatches(true));
});
$("prevPage").addEventListener("click", () => {
  if (state.page > 1) {
    state.page -= 1;
    loadWatches(false);
    $("log").scrollIntoView({ behavior: "smooth", block: "start" });
  }
});
$("nextPage").addEventListener("click", () => {
  if (state.page < pageCount()) {
    state.page += 1;
    loadWatches(false);
    $("log").scrollIntoView({ behavior: "smooth", block: "start" });
  }
});

function debounce(fn, ms) {
  let t;
  return (...args) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...args), ms);
  };
}

async function loadTarget() {
  try {
    const t = await (await fetch("/api/target")).json();
    $("kodiHost").value = t.host || "";
    $("kodiPort").value = t.port || 8080;
    $("kodiUser").value = t.user || "";
    return t;
  } catch (_) {
    return null;
  }
}

function renderBoxes(items, target) {
  const list = $("discoverList");
  if (!items || !items.length) {
    list.innerHTML = "";
    return;
  }
  list.innerHTML = items.map((b) => {
    const active = target && b.host === target.host && b.port === target.port;
    const label = `${b.host}:${b.port}${b.auth_required ? " · auth" : ""}`;
    return `<button type="button" class="box-chip ${active ? "active" : ""}" data-host="${b.host}" data-port="${b.port}">${label}</button>`;
  }).join("");
  list.querySelectorAll(".box-chip").forEach((btn) => {
    btn.addEventListener("click", () => {
      $("kodiHost").value = btn.dataset.host;
      $("kodiPort").value = btn.dataset.port;
    });
  });
}

function closeDiscover() {
  const panel = $("discover");
  if (panel) panel.hidden = true;
}

async function discoverKodi() {
  const panel = $("discover");
  panel.hidden = false;
  $("discoverTitle").textContent = "Looking for Kodi…";
  $("discoverHint").textContent = "SSDP + LAN scan on :8080";
  try {
    const data = await (await fetch("/api/discover")).json();
    const items = data.items || [];
    const target = data.target || {};
    renderBoxes(items, target);
    if (target.host) {
      $("kodiHost").value = target.host;
      $("kodiPort").value = target.port || 8080;
      $("kodiUser").value = target.user || $("kodiUser").value;
    }
    const live = items.filter((b) => b.reachable || b.active);
    $("discoverTitle").textContent = live.length
      ? `Found ${live.length} Kodi ${live.length === 1 ? "box" : "boxes"}`
      : "No extra boxes on the LAN";
    $("discoverHint").textContent = live.length
      ? "Tap a chip, then Use this box"
      : (target.host ? `Still using ${target.host}:${target.port || 8080}` : "Enter the IP and port");
  } catch (err) {
    $("discoverTitle").textContent = "Discovery failed";
    $("discoverHint").textContent = String(err && err.message ? err.message : err);
  }
}

$("scanBtn").addEventListener("click", () => discoverKodi());
$("discoverClose").addEventListener("click", closeDiscover);
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") closeDiscover();
});
$("targetForm").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const body = {
    host: $("kodiHost").value.trim(),
    port: Number($("kodiPort").value) || 8080,
    user: $("kodiUser").value.trim(),
    pass: $("kodiPass").value,
  };
  if (!body.host) return;
  const res = await fetch("/api/target", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    $("discoverHint").textContent = "Could not switch target";
    return;
  }
  $("kodiPass").value = "";
  $("discoverHint").textContent = "Switched. Polling the new box…";
  closeDiscover();
  setTimeout(refreshNow, 500);
});

async function loadBoxes() {
  const el = $("box");
  if (!el) return;
  try {
    const data = await (await fetch("/api/boxes")).json();
    const cur = el.value || "all";
    const items = data.items || [];
    el.innerHTML = `<option value="all">All boxes</option>` + items.map((b) => {
      const label = b.name && b.name !== b.host ? `${b.name} (${b.host})` : (b.name || b.host);
      return `<option value="${escapeHtml(b.host)}">${escapeHtml(label)}</option>`;
    }).join("");
    el.value = [...el.options].some((o) => o.value === cur) ? cur : "all";
  } catch (_) {}
}

refreshNow();
refreshStats();
loadBoxes();
loadWatches(false);
loadHeat();
connectSSE();
loadTarget();
setInterval(refreshNow, 4000);

function setRange(kind) {
  document.querySelectorAll("#rangeChips .chip").forEach((b) => b.classList.toggle("on", b.dataset.range === kind));
  const now = new Date();
  if (kind === "today") {
    $("from").value = ymd(now);
    $("to").value = ymd(now);
  } else if (kind === "week") {
    const from = new Date(now);
    from.setDate(now.getDate() - ((now.getDay() + 6) % 7));
    $("from").value = ymd(from);
    $("to").value = ymd(now);
  } else if (kind === "month") {
    $("from").value = ymd(new Date(now.getFullYear(), now.getMonth(), 1));
    $("to").value = ymd(now);
  } else {
    $("from").value = "";
    $("to").value = "";
  }
  state.page = 1;
  loadWatches(true);
  loadHeat();
}

$("rangeChips") && $("rangeChips").addEventListener("click", (ev) => {
  const btn = ev.target.closest("[data-range]");
  if (btn) setRange(btn.dataset.range);
});

function csv(arr) { return (arr || []).join(", "); }
function splitCSV(s) {
  return String(s || "").split(",").map((x) => x.trim()).filter(Boolean);
}

async function loadIgnore() {
  try {
    const r = await (await fetch("/api/ignore")).json();
    $("ignMin").value = r.min_seconds || "";
    $("ignKinds").value = csv(r.kinds);
    $("ignTitle").value = csv(r.title_contains);
    $("ignFile").value = csv(r.file_contains);
    $("ignPlugin").value = csv(r.plugins);
  } catch (_) {}
}

$("ignoreBtn") && $("ignoreBtn").addEventListener("click", () => {
  const p = $("ignorePanel");
  p.hidden = !p.hidden;
  if (!p.hidden) loadIgnore();
});
$("ignoreClose") && $("ignoreClose").addEventListener("click", () => { $("ignorePanel").hidden = true; });
$("ignoreForm") && $("ignoreForm").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const body = {
    min_seconds: Number($("ignMin").value) || 0,
    kinds: splitCSV($("ignKinds").value),
    title_contains: splitCSV($("ignTitle").value),
    file_contains: splitCSV($("ignFile").value),
    plugins: splitCSV($("ignPlugin").value),
  };
  await fetch("/api/ignore", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
  $("ignorePanel").hidden = true;
});

(function prefs() {
  const root = document.documentElement;
  const wideOn = localStorage.getItem("kk-wide") !== "0";
  const lightOn = localStorage.getItem("kk-light") === "1";
  root.classList.toggle("narrow-art", !wideOn);
  root.classList.toggle("light", lightOn);
  const wideEl = document.getElementById("wideToggle");
  const lightEl = document.getElementById("lightToggle");
  if (wideEl) wideEl.checked = wideOn;
  if (lightEl) lightEl.checked = lightOn;

  const menu = document.getElementById("appMenu");
  const btn = document.getElementById("menuBtn");
  const pop = document.getElementById("menuPop");
  function closeMenu() {
    if (!pop || !btn) return;
    pop.hidden = true;
    btn.setAttribute("aria-expanded", "false");
  }
  if (btn && pop) {
    btn.addEventListener("click", (ev) => {
      ev.stopPropagation();
      const open = pop.hidden;
      pop.hidden = !open;
      btn.setAttribute("aria-expanded", open ? "true" : "false");
    });
    document.addEventListener("click", (ev) => {
      if (menu && !menu.contains(ev.target)) closeMenu();
    });
    document.addEventListener("keydown", (ev) => {
      if (ev.key === "Escape") closeMenu();
    });
  }
  if (wideEl) {
    wideEl.addEventListener("change", () => {
      localStorage.setItem("kk-wide", wideEl.checked ? "1" : "0");
      root.classList.toggle("narrow-art", !wideEl.checked);
      if (typeof refreshNow === "function") refreshNow();
      if (typeof loadWatches === "function") loadWatches(true);
    });
  }
  if (lightEl) {
    lightEl.addEventListener("change", () => {
      localStorage.setItem("kk-light", lightEl.checked ? "1" : "0");
      root.classList.toggle("light", lightEl.checked);
    });
  }
})();

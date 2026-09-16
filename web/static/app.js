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

function setStatus(mode, text) {
  const el = $("status");
  el.className = "status " + mode;
  $("statusText").textContent = text;
}

function renderNow(now) {
  const card = $("nowCard");
  if (!now || !now.connected) {
    setStatus("bad", "Kodi offline");
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
  $("nowEyebrow").textContent = now.paused ? "Paused" : "Now playing";
  $("nowTitle").textContent = titleOf(media);
  const boxBit = now.box_name || now.box_host;
  $("nowMeta").textContent = [nowMetaLine(media), boxBit ? "on " + boxBit : ""].filter(Boolean).join(" · ");
  const live = media.kind === "iptv" || (!now.runtime_seconds && now.playing);
  $("nowBar").style.width = live ? "100%" : `${Math.min(100, now.progress_percent || 0)}%`;
  $("nowPos").textContent = fmtTime(now.watched_seconds || now.position_seconds);
  $("nowDur").textContent = live ? "LIVE" : fmtTime(now.runtime_seconds);
  $("nowState").textContent = now.paused ? "paused" : (live ? `live · ${fmtTime(now.watched_seconds)}` : `watched ${fmtTime(now.watched_seconds)}`);
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
    const boxBit = boxLabel ? ` · ${escapeHtml(boxLabel)}` : "";
    const subtitle = isIdle
      ? `${fmtWhen(w.started_at)} → ${fmtWhen(w.ended_at)} · nothing playing${boxBit}`
      : isLive
        ? `${fmtWhen(w.started_at)} · live TV${boxBit}`
        : `${fmtWhen(w.started_at)} · reached ${fmtTime(w.position_seconds)}${w.runtime_seconds ? " / " + fmtTime(w.runtime_seconds) : ""} ${w.completed ? " · finished" : ""}${boxBit}`;
    return `<article class="card">
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

async function loadHeat() {
  const res = await fetch("/api/heatmap?days=365");
  const data = await res.json();
  const days = data.items || [];
  const root = $("heat");
  if (!days.length) {
    root.innerHTML = "";
    return;
  }
  const first = new Date(days[0].date + "T12:00:00Z");
  const pad = first.getUTCDay();
  const max = Math.max(1, ...days.map((d) => d.watched_seconds));
  const cells = [];
  for (let i = 0; i < pad; i++) cells.push("<i></i>");
  let activeDays = 0;
  for (const d of days) {
    if (d.watched_seconds > 0) activeDays += 1;
    let lvl = 0;
    if (d.watched_seconds > 0) {
      const r = d.watched_seconds / max;
      lvl = r < 0.25 ? 1 : r < 0.5 ? 2 : r < 0.75 ? 3 : 4;
    }
    const tip = `${d.date}: ${fmtTime(d.watched_seconds)} watched` +
      (d.idle_seconds ? `, ${fmtTime(d.idle_seconds)} idle` : "");
    cells.push(`<i class="${lvl ? "l" + lvl : ""}" title="${tip}"></i>`);
  }
  root.innerHTML = cells.join("");
  $("heatLegend").textContent = `${activeDays} day${activeDays === 1 ? "" : "s"} with playback`;
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

async function discoverKodi(forceOpen) {
  const panel = $("discover");
  $("discoverTitle").textContent = "Looking for Kodi…";
  $("discoverHint").textContent = "SSDP + LAN scan on :8080";
  if (forceOpen) panel.hidden = false;
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
    if (forceOpen || live.length || target.host) panel.hidden = false;
  } catch (err) {
    $("discoverTitle").textContent = "Discovery failed";
    $("discoverHint").textContent = String(err && err.message ? err.message : err);
    panel.hidden = false;
  }
}

$("scanBtn").addEventListener("click", () => discoverKodi(true));
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
discoverKodi(false);
setInterval(refreshNow, 4000);

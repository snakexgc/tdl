// Downloads view: AriaNg iframe for aria2 mode, internal download table otherwise.
import { state } from "./state.js";
import { api } from "./api.js";
import { escapeHTML, escapeAttr, formatBytes, formatTime } from "./utils.js";
import { navigate } from "./router.js";
import { loadConfig } from "./config.js";

const internalDownloadRefreshMS = 1000;

export function initDownloads() {
  document.getElementById("reload-aria2").addEventListener("click", () => loadDownloads(true));
  document.getElementById("aria2-retry-check").addEventListener("click", () => loadDownloads(true));
  document.getElementById("aria2-open-config").addEventListener("click", async () => {
    navigate("config");
    await loadConfig();
    requestAnimationFrame(() => {
      const input = document.querySelector('#config-form [data-path="aria2.rpc_url"]');
      if (input) {
        input.focus();
        input.scrollIntoView({ block: "center" });
      }
    });
  });

  document.getElementById("internal-select-visible").addEventListener("change", (event) => {
    if (event.target.checked) {
      state.internalDownloads.forEach((item) => {
        const id = internalDownloadID(item);
        if (id) state.selectedInternalDownloads.add(id);
      });
    } else {
      state.selectedInternalDownloads.clear();
    }
    renderInternalDownloads();
  });
  document.getElementById("internal-select-all").addEventListener("click", () => selectInternalDownloads("all"));
  document.getElementById("internal-select-complete").addEventListener("click", () => selectInternalDownloads("complete"));
  document.getElementById("internal-select-unfinished").addEventListener("click", () => selectInternalDownloads("unfinished"));
  document.getElementById("internal-select-error").addEventListener("click", () => selectInternalDownloads("error"));
  document.getElementById("internal-select-clear").addEventListener("click", () => selectInternalDownloads("clear"));
  document.getElementById("internal-start-selected").addEventListener("click", () => runInternalDownloadBulkAction("start"));
  document.getElementById("internal-pause-selected").addEventListener("click", () => runInternalDownloadBulkAction("pause"));
  document.getElementById("internal-delete-selected").addEventListener("click", () => runInternalDownloadBulkAction("delete"));

  document.getElementById("internal-download-body").addEventListener("click", async (event) => {
    const button = event.target.closest("[data-internal-action]");
    if (!button) return;
    const action = button.dataset.internalAction;
    const id = button.dataset.internalId;
    if (!id) return;
    if (action === "delete" && !confirm(`删除下载任务 ${id}？未完成的本地临时文件会一并删除。`)) return;
    await runInternalDownloadAction(action, [id]);
  });

  document.getElementById("internal-download-body").addEventListener("change", (event) => {
    const checkbox = event.target.closest("[data-internal-check]");
    if (!checkbox) return;
    if (checkbox.checked) {
      state.selectedInternalDownloads.add(checkbox.dataset.internalCheck);
    } else {
      state.selectedInternalDownloads.delete(checkbox.dataset.internalCheck);
    }
    updateInternalSelectionState();
  });
}

export async function loadDownloads(force = false) {
  let status = null;
  try {
    status = await api("/api/status");
    state.downloaderMode = status.downloader && status.downloader.mode ? status.downloader.mode : "aria2";
  } catch {
    state.downloaderMode = "aria2";
  }

  if (state.downloaderMode === "internal") {
    state.aria2Loaded = false;
    const frame = document.getElementById("aria2-frame");
    const guide = document.getElementById("aria2-guide");
    if (frame) {
      frame.hidden = true;
      frame.removeAttribute("src");
    }
    if (guide) guide.hidden = true;
    document.getElementById("internal-downloads").hidden = false;
    await loadInternalDownloads();
    startInternalDownloadPolling();
    return;
  }

  stopInternalDownloadPolling();
  document.getElementById("internal-downloads").hidden = true;
  await loadAria2Frame(force);
}

async function loadAria2Frame(force = false) {
  if (state.aria2Loaded && !force) return;
  if (force) {
    state.aria2Loaded = false;
    document.getElementById("aria2-frame").removeAttribute("src");
  }
  showAria2Guide({
    message: "正在检查 aria2 配置...",
    checking: true,
  });

  let check;
  try {
    check = await api("/api/aria2/check");
  } catch (error) {
    showAria2Guide({
      message: "无法检查 aria2 配置。",
      error: error.message,
    });
    return;
  }

  if (!check.ok) {
    showAria2Guide(check);
    return;
  }

  const protocol = location.protocol.replace(":", "");
  const port = location.port || (protocol === "https" ? "443" : "80");
  const query = new URLSearchParams({
    protocol,
    host: location.hostname,
    port,
    interface: "aria2/jsonrpc",
  });
  hideAria2Guide();
  const frame = document.getElementById("aria2-frame");
  frame.src = `/aria2ng.html#!/settings/rpc/set?${query.toString()}`;
  state.aria2Loaded = true;
}

function showAria2Guide(result = {}) {
  const guide = document.getElementById("aria2-guide");
  const frame = document.getElementById("aria2-frame");
  const message = document.getElementById("aria2-guide-message");
  frame.hidden = true;
  guide.hidden = false;
  if (!result.checking) {
    frame.removeAttribute("src");
  }
  const parts = [];
  if (result.message) parts.push(result.message);
  if (result.rpc_url) parts.push(`当前 aria2.rpc_url：${result.rpc_url}`);
  if (result.error) parts.push(`错误详情：${result.error}`);
  message.textContent = parts.join("\n") || "请先完成 aria2 配置。";
}

function hideAria2Guide() {
  document.getElementById("aria2-guide").hidden = true;
  document.getElementById("aria2-frame").hidden = false;
}

function startInternalDownloadPolling() {
  if (state.internalDownloadPoll) return;
  state.internalDownloadPoll = window.setInterval(() => {
    if (!document.getElementById("view-downloads").classList.contains("active") || state.downloaderMode !== "internal") {
      stopInternalDownloadPolling();
      return;
    }
    loadInternalDownloads({ silent: true });
  }, internalDownloadRefreshMS);
}

export function stopInternalDownloadPolling() {
  if (!state.internalDownloadPoll) return;
  window.clearInterval(state.internalDownloadPoll);
  state.internalDownloadPoll = null;
}

async function loadInternalDownloads(options = {}) {
  if (state.internalDownloadLoading) return;
  state.internalDownloadLoading = true;
  const silent = Boolean(options.silent);
  const body = document.getElementById("internal-download-body");
  if (!silent) {
    setInternalDownloadStatus("");
    body.innerHTML = `<tr><td colspan="7" class="empty">正在加载...</td></tr>`;
  }
  try {
    const data = await api("/api/internal-downloads");
    state.internalDownloads = updateInternalDownloadSpeeds(data.items || []);
    pruneInternalDownloadSelection();
    renderInternalDownloads();
  } catch (error) {
    if (!silent) {
      state.internalDownloads = [];
      state.internalDownloadSamples.clear();
      state.selectedInternalDownloads.clear();
      body.innerHTML = `<tr><td colspan="7" class="empty">加载失败</td></tr>`;
      updateInternalSelectionState();
    }
    setInternalDownloadStatus(error.message, "error");
  } finally {
    state.internalDownloadLoading = false;
  }
}

function renderInternalDownloads() {
  const body = document.getElementById("internal-download-body");
  if (!state.internalDownloads.length) {
    body.innerHTML = `<tr><td colspan="7" class="empty">没有内部下载任务</td></tr>`;
    updateInternalSelectionState();
    return;
  }
  body.innerHTML = state.internalDownloads.map(renderInternalDownloadRow).join("");
  updateInternalSelectionState();
}

export function internalDownloadID(item) {
  return item.id || item.task_id || item.file_name || "";
}

function updateInternalDownloadSpeeds(items) {
  const now = Date.now();
  const seen = new Set();
  items.forEach((item) => {
    const id = internalDownloadID(item);
    if (!id) return;
    seen.add(id);
    const completed = Number(item.completed || 0);
    const total = Number(item.total || 0);
    const status = item.status || "queued";
    const previous = state.internalDownloadSamples.get(id);
    let speed = previous ? previous.speed : 0;

    if (previous && now > previous.sampledAt) {
      const elapsed = (now - previous.sampledAt) / 1000;
      const delta = completed - previous.completed;
      if (status === "active" && elapsed > 0 && delta >= 0) {
        speed = delta / elapsed;
      } else if (status !== "active") {
        speed = 0;
      }
    } else if (status !== "active") {
      speed = 0;
    }
    if (total > 0 && completed >= total) {
      speed = 0;
    }

    item.speed_bps = speed;
    state.internalDownloadSamples.set(id, {
      completed,
      sampledAt: now,
      speed,
    });
  });

  Array.from(state.internalDownloadSamples.keys()).forEach((id) => {
    if (!seen.has(id)) state.internalDownloadSamples.delete(id);
  });
  return items;
}

function renderInternalDownloadRow(item) {
  const id = internalDownloadID(item);
  const total = Number(item.total || 0);
  const completed = Number(item.completed || 0);
  const pct = total > 0 ? Math.min(100, Math.max(0, (completed / total) * 100)) : 0;
  const status = item.status || "queued";
  const error = item.error ? `<div class="subtle bad-text">${escapeHTML(item.error)}</div>` : "";
  const speed = Number(item.speed_bps || 0);
  const speedText = status === "active" ? `${formatBytes(speed)}/s` : "-";
  const selected = state.selectedInternalDownloads.has(id) ? "checked" : "";
  return `
    <tr>
      <td class="select-col"><input type="checkbox" data-internal-check="${escapeAttr(id)}" ${selected} aria-label="选择 ${escapeAttr(item.file_name || id)}"></td>
      <td>
        <strong>${escapeHTML(item.file_name || id)}</strong>
        <div class="mono subtle">${escapeHTML(id)}</div>
      </td>
      <td><span class="pill ${internalStatusClass(status)}">${escapeHTML(internalStatusLabel(status))}</span>${error}</td>
      <td>
        <div class="download-progress">
          <div class="download-progress-meta">
            <span>${pct.toFixed(1)}%</span>
            <span>${formatBytes(completed)} / ${formatBytes(total)}</span>
          </div>
          <div class="download-progress-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${pct.toFixed(1)}">
            <span style="width: ${pct.toFixed(2)}%"></span>
          </div>
          <div class="download-speed">速度：${escapeHTML(speedText)}</div>
        </div>
      </td>
      <td class="mono">${escapeHTML(item.path || "-")}</td>
      <td class="time-cell">${formatTime(item.updated_at || item.created_at)}</td>
      <td>
        <div class="row-actions">
          ${status === "paused" || status === "error" ? `<button class="btn primary compact" data-internal-action="start" data-internal-id="${escapeAttr(id)}">开始</button>` : ""}
          ${status !== "complete" && status !== "paused" ? `<button class="btn secondary compact" data-internal-action="pause" data-internal-id="${escapeAttr(id)}">暂停</button>` : ""}
          <button class="btn danger compact" data-internal-action="delete" data-internal-id="${escapeAttr(id)}">删除</button>
        </div>
      </td>
    </tr>
  `;
}

function selectInternalDownloads(mode) {
  state.selectedInternalDownloads.clear();
  if (mode !== "clear") {
    state.internalDownloads.forEach((item) => {
      const id = internalDownloadID(item);
      const status = item.status || "queued";
      if (!id) return;
      if (mode === "all" || mode === status || (mode === "unfinished" && status !== "complete")) {
        state.selectedInternalDownloads.add(id);
      }
    });
  }
  renderInternalDownloads();
  setInternalDownloadStatus(`已选中 ${state.selectedInternalDownloads.size} 个任务。`);
}

async function runInternalDownloadBulkAction(action) {
  const ids = Array.from(state.selectedInternalDownloads);
  if (!ids.length) {
    setInternalDownloadStatus("请先选择需要处理的内部下载任务。", "error");
    return;
  }
  if (action === "delete" && !confirm(`删除选中的 ${ids.length} 个下载任务？未完成的本地文件会一并删除。`)) return;
  await runInternalDownloadAction(action, ids);
}

async function runInternalDownloadAction(action, ids) {
  const uniqueIDs = Array.from(new Set(ids)).filter(Boolean);
  if (!uniqueIDs.length) {
    setInternalDownloadStatus("请先选择需要处理的内部下载任务。", "error");
    return;
  }
  setInternalDownloadStatus(internalActionPending(action));
  try {
    const data = await api("/api/internal-downloads/actions", {
      method: "POST",
      body: JSON.stringify({ action, ids: uniqueIDs }),
    });
    if (action === "delete") {
      uniqueIDs.forEach((id) => state.selectedInternalDownloads.delete(id));
    }
    await loadInternalDownloads();
    const result = data.result || {};
    const errors = result.errors && result.errors.length ? `；失败 ${result.errors.length} 项：${result.errors.join("；")}` : "";
    setInternalDownloadStatus(`已处理 ${result.changed || 0} 个任务，跳过 ${result.skipped || 0} 个${errors}`, errors ? "error" : "success");
  } catch (error) {
    setInternalDownloadStatus(error.message, "error");
  }
}

function internalActionPending(action) {
  if (action === "pause") return "正在暂停任务...";
  if (action === "start") return "正在加入队列...";
  if (action === "delete") return "正在删除任务...";
  return "正在处理任务...";
}

export function internalStatusLabel(status) {
  switch (status) {
    case "queued":
      return "排队中";
    case "active":
      return "下载中";
    case "paused":
      return "已暂停";
    case "complete":
      return "已完成";
    case "error":
      return "出错";
    default:
      return status || "-";
  }
}

export function internalStatusClass(status) {
  switch (status) {
    case "complete":
      return "";
    case "error":
      return "bad";
    default:
      return "warn";
  }
}

function setInternalDownloadStatus(message, kind = "") {
  const status = document.getElementById("internal-download-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

function pruneInternalDownloadSelection() {
  const ids = new Set(state.internalDownloads.map(internalDownloadID).filter(Boolean));
  state.selectedInternalDownloads = new Set(Array.from(state.selectedInternalDownloads).filter((id) => ids.has(id)));
}

function updateInternalSelectionState() {
  pruneInternalDownloadSelection();
  const ids = state.internalDownloads.map(internalDownloadID).filter(Boolean);
  const selectedVisible = ids.filter((id) => state.selectedInternalDownloads.has(id)).length;
  const selectVisible = document.getElementById("internal-select-visible");
  if (selectVisible) {
    selectVisible.checked = ids.length > 0 && selectedVisible === ids.length;
    selectVisible.indeterminate = selectedVisible > 0 && selectedVisible < ids.length;
  }

  const count = state.selectedInternalDownloads.size;
  const countLabel = document.getElementById("internal-selection-count");
  if (countLabel) countLabel.textContent = `已选 ${count} 项`;
  ["internal-start-selected", "internal-pause-selected", "internal-delete-selected"].forEach((id) => {
    const button = document.getElementById(id);
    if (button) button.disabled = count === 0;
  });
}

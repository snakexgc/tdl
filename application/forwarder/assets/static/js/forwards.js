// Forward monitoring view: lists the persistent forward queue (pending, running,
// retrying and recently finished jobs) with progress, origin/destination, and
// pause/resume/retry/delete controls.
import { state } from "./state.js";
import { api } from "./api.js";
import { escapeHTML, escapeAttr, formatBytes, formatTime } from "./utils.js";

const forwardRefreshMS = 1000;

export function initForwards() {
  document.getElementById("forward-reload").addEventListener("click", () => loadForwards());

  document.getElementById("forward-select-visible").addEventListener("change", (event) => {
    if (event.target.checked) {
      state.forwardTasks.forEach((item) => {
        const id = forwardID(item);
        if (id) state.selectedForwards.add(id);
      });
    } else {
      state.selectedForwards.clear();
    }
    renderForwards();
  });
  document.getElementById("forward-select-all").addEventListener("click", () => selectForwards("all"));
  document.getElementById("forward-select-active").addEventListener("click", () => selectForwards("active"));
  document.getElementById("forward-select-clear").addEventListener("click", () => selectForwards("clear"));
  document.getElementById("forward-resume-selected").addEventListener("click", () => runForwardBulkAction("resume"));
  document.getElementById("forward-pause-selected").addEventListener("click", () => runForwardBulkAction("pause"));
  document.getElementById("forward-delete-selected").addEventListener("click", () => runForwardBulkAction("delete"));

  document.getElementById("forward-body").addEventListener("click", async (event) => {
    const button = event.target.closest("[data-forward-action]");
    if (!button) return;
    const action = button.dataset.forwardAction;
    const id = button.dataset.forwardId;
    if (!id) return;
    if (action === "delete" && !confirm(`删除转发任务 ${id}？正在转发的任务会被立即停止。`)) return;
    await runForwardAction(action, [id]);
  });

  document.getElementById("forward-body").addEventListener("change", (event) => {
    const checkbox = event.target.closest("[data-forward-check]");
    if (!checkbox) return;
    if (checkbox.checked) {
      state.selectedForwards.add(checkbox.dataset.forwardCheck);
    } else {
      state.selectedForwards.delete(checkbox.dataset.forwardCheck);
    }
    updateForwardSelectionState();
  });
}

export async function loadForwards(options = {}) {
  if (state.forwardLoading) return;
  state.forwardLoading = true;
  const silent = Boolean(options.silent);
  const body = document.getElementById("forward-body");
  if (!silent) {
    setForwardStatus("");
    body.innerHTML = `<tr><td colspan="7" class="empty">正在加载...</td></tr>`;
  }
  try {
    const data = await api("/api/forwards");
    state.forwardTasks = Array.isArray(data.items) ? data.items : [];
    setRunningBadge(Number(data.running || 0));
    pruneForwardSelection();
    renderForwards();
    startForwardPolling();
  } catch (error) {
    if (!silent) {
      state.forwardTasks = [];
      state.selectedForwards.clear();
      setRunningBadge(0);
      body.innerHTML = `<tr><td colspan="7" class="empty">加载失败</td></tr>`;
      updateForwardSelectionState();
    }
    setForwardStatus(error.message, "error");
  } finally {
    state.forwardLoading = false;
  }
}

function startForwardPolling() {
  if (state.forwardPoll) return;
  state.forwardPoll = window.setInterval(() => {
    if (!document.getElementById("view-forwards").classList.contains("active")) {
      stopForwardPolling();
      return;
    }
    loadForwards({ silent: true });
  }, forwardRefreshMS);
}

export function stopForwardPolling() {
  if (!state.forwardPoll) return;
  window.clearInterval(state.forwardPoll);
  state.forwardPoll = null;
}

function renderForwards() {
  renderForwardSummary();
  const body = document.getElementById("forward-body");
  if (!state.forwardTasks.length) {
    body.innerHTML = `<tr><td colspan="7" class="empty">暂无转发任务</td></tr>`;
    updateForwardSelectionState();
    return;
  }
  body.innerHTML = state.forwardTasks.map(renderForwardRow).join("");
  updateForwardSelectionState();
}

function renderForwardSummary() {
  const counts = { waiting: 0, running: 0, paused: 0, failed: 0 };
  state.forwardTasks.forEach((item) => {
    switch (item.status) {
      case "queued":
      case "retrying":
        counts.waiting += 1;
        break;
      case "running":
        counts.running += 1;
        break;
      case "paused":
        counts.paused += 1;
        break;
      case "error":
        counts.failed += 1;
        break;
      default:
        break;
    }
  });
  setSummaryValue("forward-stat-waiting", counts.waiting);
  setSummaryValue("forward-stat-running", counts.running);
  setSummaryValue("forward-stat-paused", counts.paused);
  setSummaryValue("forward-stat-failed", counts.failed);
}

function setSummaryValue(id, value) {
  const target = document.getElementById(id);
  if (target) target.textContent = String(value);
}

function setRunningBadge(running) {
  const badge = document.getElementById("forward-running-badge");
  if (!badge) return;
  badge.textContent = `进行中 ${running}`;
  badge.classList.toggle("warn", running > 0);
}

export function forwardID(item) {
  return item && item.id ? item.id : "";
}

function renderForwardRow(item) {
  const id = forwardID(item);
  const status = item.status || "queued";
  const progress = forwardProgress(item);
  const selected = state.selectedForwards.has(id) ? "checked" : "";

  let note = "";
  if (status === "retrying") {
    note = `<div class="subtle">重试中（已重试 ${Number(item.attempts || 0)} 次），稍后自动重试</div>`;
  } else if (item.error) {
    note = `<div class="subtle bad-text">${escapeHTML(item.error)}</div>`;
  }

  return `
    <tr>
      <td class="select-col"><input type="checkbox" data-forward-check="${escapeAttr(id)}" ${selected} aria-label="选择任务 ${escapeAttr(id)}"></td>
      <td>
        <div class="forward-path">${renderForwardPath(item)}</div>
        <div class="mono subtle">${escapeHTML(id)}</div>
      </td>
      <td>
        <div>${escapeHTML(forwardSourceLabel(item.source))}</div>
        <div class="subtle">${escapeHTML(forwardModeLabel(item.mode))}</div>
      </td>
      <td><span class="pill ${forwardStatusClass(status)}">${escapeHTML(forwardStatusLabel(status))}</span>${note}</td>
      <td>
        <div class="download-progress">
          <div class="download-progress-meta">
            <span>${progress.pct.toFixed(1)}%</span>
            <span>${escapeHTML(progress.label)}</span>
          </div>
          <div class="download-progress-bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow="${progress.pct.toFixed(1)}">
            <span style="width: ${progress.pct.toFixed(2)}%"></span>
          </div>
        </div>
      </td>
      <td class="time-cell">${formatTime(item.updated_at || item.created_at)}</td>
      <td>
        <div class="row-actions">
          ${forwardRowActions(status, id)}
        </div>
      </td>
    </tr>
  `;
}

function forwardProgress(item) {
  const status = item.status || "queued";
  const total = Number(item.total || 0);
  const done = Number(item.done || 0);
  const cloneTotal = Number(item.clone_total || 0);
  const cloneDone = Number(item.clone_done || 0);
  if (status === "running" && cloneTotal > 0) {
    return {
      pct: Math.min(100, Math.max(0, (cloneDone / cloneTotal) * 100)),
      label: `${formatBytes(cloneDone)} / ${formatBytes(cloneTotal)}`,
    };
  }
  if (status === "done") {
    return { pct: 100, label: `${total || 1} / ${total || 1} 条` };
  }
  if (total > 0) {
    return { pct: Math.min(100, Math.max(0, (done / total) * 100)), label: `${done} / ${total} 条` };
  }
  return { pct: 0, label: "-" };
}

function renderForwardPath(item) {
  const origin = forwardLabel(item.origin_name, item.source_link, item.source_peer_id, "未知来源");
  const destination = forwardLabel(item.destination_name, item.destination, 0, "收藏夹");
  return `<span class="forward-peer">${escapeHTML(origin)}</span><span class="forward-arrow">→</span><span class="forward-peer">${escapeHTML(destination)}</span>`;
}

function forwardLabel(name, fallback, id, empty) {
  const trimmedName = (name || "").trim();
  if (trimmedName) return trimmedName;
  const trimmedFallback = (fallback || "").trim();
  if (trimmedFallback) return trimmedFallback;
  if (id) return `ID ${id}`;
  return empty;
}

function forwardRowActions(status, id) {
  const buttons = [];
  if (status === "paused") {
    buttons.push(`<button class="btn primary compact" data-forward-action="resume" data-forward-id="${escapeAttr(id)}">继续</button>`);
  }
  if (status === "error") {
    buttons.push(`<button class="btn primary compact" data-forward-action="retry" data-forward-id="${escapeAttr(id)}">重试</button>`);
  }
  if (status === "queued" || status === "retrying") {
    buttons.push(`<button class="btn secondary compact" data-forward-action="pause" data-forward-id="${escapeAttr(id)}">暂停</button>`);
  }
  buttons.push(`<button class="btn danger compact" data-forward-action="delete" data-forward-id="${escapeAttr(id)}">删除</button>`);
  return buttons.join("");
}

function selectForwards(mode) {
  state.selectedForwards.clear();
  if (mode !== "clear") {
    state.forwardTasks.forEach((item) => {
      const id = forwardID(item);
      const status = item.status || "";
      if (!id) return;
      const active = status === "queued" || status === "running" || status === "paused" || status === "retrying";
      if (mode === "all" || (mode === "active" && active)) {
        state.selectedForwards.add(id);
      }
    });
  }
  renderForwards();
  setForwardStatus(`已选中 ${state.selectedForwards.size} 个任务。`);
}

async function runForwardBulkAction(action) {
  const ids = Array.from(state.selectedForwards);
  if (!ids.length) {
    setForwardStatus("请先选择需要处理的转发任务。", "error");
    return;
  }
  if (action === "delete" && !confirm(`删除选中的 ${ids.length} 个转发任务？正在转发的任务会被立即停止。`)) return;
  await runForwardAction(action, ids);
}

async function runForwardAction(action, ids) {
  const uniqueIDs = Array.from(new Set(ids)).filter(Boolean);
  if (!uniqueIDs.length) {
    setForwardStatus("请先选择需要处理的转发任务。", "error");
    return;
  }
  setForwardStatus(forwardActionPending(action));
  try {
    const data = await api("/api/forwards/actions", {
      method: "POST",
      body: JSON.stringify({ action, ids: uniqueIDs }),
    });
    if (action === "delete") {
      uniqueIDs.forEach((id) => state.selectedForwards.delete(id));
    }
    await loadForwards();
    const result = data.result || {};
    const errors = result.errors && result.errors.length ? `；失败 ${result.errors.length} 项：${result.errors.join("；")}` : "";
    setForwardStatus(`已处理 ${result.changed || 0} 个任务，跳过 ${result.skipped || 0} 个${errors}`, errors ? "error" : "success");
  } catch (error) {
    setForwardStatus(error.message, "error");
  }
}

function forwardActionPending(action) {
  if (action === "pause") return "正在暂停任务...";
  if (action === "resume") return "正在继续任务...";
  if (action === "retry") return "正在重试任务...";
  if (action === "delete") return "正在删除任务...";
  return "正在处理任务...";
}

function forwardSourceLabel(source) {
  switch (source) {
    case "watch":
      return "监听转发";
    case "command":
      return "命令转发";
    default:
      return source || "-";
  }
}

function forwardModeLabel(mode) {
  switch (mode) {
    case "clone":
      return "克隆模式";
    case "default":
    case "direct":
      return "直接转发";
    default:
      return mode || "-";
  }
}

export function forwardStatusLabel(status) {
  switch (status) {
    case "queued":
      return "排队中";
    case "running":
      return "转发中";
    case "paused":
      return "已暂停";
    case "retrying":
      return "重试中";
    case "done":
      return "已完成";
    case "error":
      return "失败";
    default:
      return status || "-";
  }
}

export function forwardStatusClass(status) {
  switch (status) {
    case "running":
    case "retrying":
      return "warn";
    case "error":
      return "bad";
    case "queued":
    case "paused":
      return "muted";
    default:
      return "";
  }
}

function setForwardStatus(message, kind = "") {
  const status = document.getElementById("forward-status");
  if (!status) return;
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

function pruneForwardSelection() {
  const ids = new Set(state.forwardTasks.map(forwardID).filter(Boolean));
  state.selectedForwards = new Set(Array.from(state.selectedForwards).filter((id) => ids.has(id)));
}

function updateForwardSelectionState() {
  pruneForwardSelection();
  const ids = state.forwardTasks.map(forwardID).filter(Boolean);
  const selectedVisible = ids.filter((id) => state.selectedForwards.has(id)).length;
  const selectVisible = document.getElementById("forward-select-visible");
  if (selectVisible) {
    selectVisible.checked = ids.length > 0 && selectedVisible === ids.length;
    selectVisible.indeterminate = selectedVisible > 0 && selectedVisible < ids.length;
  }

  const count = state.selectedForwards.size;
  const countLabel = document.getElementById("forward-selection-count");
  if (countLabel) countLabel.textContent = `已选 ${count} 项`;
  ["forward-resume-selected", "forward-pause-selected", "forward-delete-selected"].forEach((id) => {
    const button = document.getElementById(id);
    if (button) button.disabled = count === 0;
  });
}

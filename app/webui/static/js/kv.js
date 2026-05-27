// KV view: download-link records that can be re-submitted to the downloader.
import { state } from "./state.js";
import { api } from "./api.js";
import { collator, escapeHTML, escapeAttr, formatBytes, formatTime } from "./utils.js";
import { internalStatusClass, internalStatusLabel } from "./downloads.js";
import { loadStatus } from "./status.js";

export function initKV() {
  document.getElementById("refresh-kv").addEventListener("click", loadKV);

  document.getElementById("select-all-kv").addEventListener("change", (event) => {
    if (event.target.checked) {
      sortedKVItems().forEach((item) => state.selectedKV.add(item.id));
    } else {
      state.selectedKV.clear();
    }
    renderKVTable();
  });

  document.getElementById("select-undownloaded").addEventListener("click", () => {
    state.selectedKV.clear();
    state.kvItems.forEach((item) => {
      if (!item.downloaded) {
        state.selectedKV.add(item.id);
      }
    });
    renderKVTable();
    setKVStatus(`已选中 ${state.selectedKV.size} 个未下载条目。`);
  });

  document.getElementById("download-selected").addEventListener("click", () => {
    runKVAction("download", Array.from(state.selectedKV));
  });
  document.getElementById("delete-selected").addEventListener("click", () => {
    runKVAction("delete", Array.from(state.selectedKV));
  });

  document.querySelectorAll("#kv-table .sort-button").forEach((button) => {
    button.addEventListener("click", () => {
      setKVSort(button.dataset.sort);
    });
  });

  document.getElementById("kv-body").addEventListener("change", (event) => {
    const checkbox = event.target.closest("[data-kv-check]");
    if (!checkbox) return;
    if (checkbox.checked) {
      state.selectedKV.add(checkbox.dataset.kvCheck);
    } else {
      state.selectedKV.delete(checkbox.dataset.kvCheck);
    }
    updateKVSelectionState();
  });

  document.getElementById("kv-body").addEventListener("click", async (event) => {
    const downloadButton = event.target.closest("[data-download-link]");
    if (downloadButton) {
      await runKVAction("download", [downloadButton.dataset.downloadLink], { confirm: false });
      return;
    }

    const deleteButton = event.target.closest("[data-delete-link]");
    if (deleteButton) {
      const id = deleteButton.dataset.deleteLink;
      const suffix = state.downloaderMode === "internal" ? "关联的内部下载任务会一并移除。" : "不会删除 aria2 中已存在的下载任务。";
      if (!confirm(`删除链接记录 ${id}？${suffix}`)) return;
      await api(`/api/kv/links/${encodeURIComponent(id)}`, { method: "DELETE" });
      state.selectedKV.delete(id);
      await loadKV();
    }
  });

  bindColumnResizing();
  renderSortButtons();
}

function bindColumnResizing() {
  const table = document.getElementById("kv-table");
  const cols = Array.from(table.querySelectorAll("colgroup col"));
  table.querySelectorAll("thead th").forEach((th, index) => {
    const resizer = th.querySelector(".col-resizer");
    if (!resizer || !cols[index]) return;

    resizer.addEventListener("mousedown", (event) => {
      event.preventDefault();
      event.stopPropagation();
      const startX = event.clientX;
      const startWidth = th.offsetWidth;
      const minWidth = index === 0 ? 44 : 96;

      document.body.classList.add("is-resizing");
      const move = (moveEvent) => {
        const next = Math.max(minWidth, startWidth + moveEvent.clientX - startX);
        cols[index].style.width = `${next}px`;
      };
      const stop = () => {
        document.body.classList.remove("is-resizing");
        document.removeEventListener("mousemove", move);
        document.removeEventListener("mouseup", stop);
      };

      document.addEventListener("mousemove", move);
      document.addEventListener("mouseup", stop);
    });
  });
}

export async function loadKV() {
  await loadStatus();
  const body = document.getElementById("kv-body");
  setKVStatus("");
  body.innerHTML = `<tr><td colspan="8" class="empty">正在加载...</td></tr>`;
  try {
    const data = await api("/api/kv/links");
    if (data.status_error) {
      setKVStatus(`下载器状态查询失败：${data.status_error}`, "error");
    }
    state.kvItems = data.items || [];
    const ids = new Set(state.kvItems.map((item) => item.id));
    state.selectedKV = new Set(Array.from(state.selectedKV).filter((id) => ids.has(id)));
    renderKVTable();
  } catch (error) {
    state.kvItems = [];
    setKVStatus(error.message, "error");
    body.innerHTML = `<tr><td colspan="8" class="empty">加载失败</td></tr>`;
    updateKVSelectionState();
  }
}

function renderKVTable() {
  const body = document.getElementById("kv-body");
  renderSortButtons();
  if (!state.kvItems.length) {
    body.innerHTML = `<tr><td colspan="8" class="empty">没有下载链接记录</td></tr>`;
    updateKVSelectionState();
    return;
  }

  body.innerHTML = sortedKVItems().map(renderKVRow).join("");
  updateKVSelectionState();
}

function renderKVRow(item) {
  const expires = item.permanent ? "永久" : item.expires_at ? formatTime(item.expires_at) : "-";
  const selected = state.selectedKV.has(item.id) ? "checked" : "";
  const downloadLabel = state.downloaderMode === "internal" ? "加入队列" : "发送到 aria2";
  return `
    <tr class="${item.expired ? "row-expired" : ""}">
      <td class="select-col">
        <input type="checkbox" data-kv-check="${escapeAttr(item.id)}" aria-label="选择 ${escapeAttr(item.file_name || item.id)}" ${selected}>
      </td>
      <td>
        <strong>${escapeHTML(item.file_name || item.id)}</strong>
        <div class="mono subtle">${escapeHTML(item.id)}</div>
        <div class="subtle">${formatBytes(item.file_size || 0)}</div>
      </td>
      <td class="mono"><a href="${escapeAttr(item.url)}" target="_blank" rel="noreferrer">${escapeHTML(item.url)}</a></td>
      <td>${renderDownloadEntries(item)}</td>
      <td>${renderDownloadedState(item)}</td>
      <td class="time-cell">${formatTime(item.created_at)}</td>
      <td class="time-cell">${escapeHTML(expires)}</td>
      <td>
        <div class="row-actions">
          <button class="btn primary compact" data-download-link="${escapeAttr(item.id)}">${downloadLabel}</button>
          <button class="btn danger compact" data-delete-link="${escapeAttr(item.id)}">删除</button>
        </div>
      </td>
    </tr>
  `;
}

function renderDownloadEntries(item) {
  if (state.downloaderMode === "internal") {
    return renderInternalEntries(item);
  }
  return renderAria2Entries(item);
}

function renderAria2Entries(item) {
  const entries = item.aria2 || [];
  if (!entries.length) {
    return `<span class="pill warn">尚未发送</span>`;
  }
  return entries.map((entry) => {
    const status = entry.status || "registered";
    const progress = entry.total ? ` ${formatBytes(entry.completed)} / ${formatBytes(entry.total)}` : "";
    const error = entry.error ? `<div class="subtle bad-text">${escapeHTML(entry.error)}</div>` : "";
    return `
      <div class="aria2-entry">
        <div class="mono">${escapeHTML(entry.gid || "-")} <span class="pill ${aria2StatusClass(status)}">${escapeHTML(status)}</span></div>
        <div class="subtle">${escapeHTML(progress)}</div>
        ${error}
      </div>
    `;
  }).join("");
}

function renderInternalEntries(item) {
  const entries = item.internal || [];
  if (!entries.length) {
    return `<span class="pill warn">尚未加入</span>`;
  }
  return entries.map((entry) => {
    const status = entry.status || "queued";
    const progress = entry.total ? ` ${formatBytes(entry.completed)} / ${formatBytes(entry.total)}` : "";
    const error = entry.error ? `<div class="subtle bad-text">${escapeHTML(entry.error)}</div>` : "";
    return `
      <div class="aria2-entry">
        <div class="mono">${escapeHTML(entry.id || "-")} <span class="pill ${internalStatusClass(status)}">${escapeHTML(internalStatusLabel(status))}</span></div>
        <div class="subtle">${escapeHTML(progress)}</div>
        ${error}
      </div>
    `;
  }).join("");
}

function renderDownloadedState(item) {
  if (item.downloaded) {
    return `<span class="pill">已下载</span>`;
  }
  if (item.expired) {
    return `<span class="pill bad">已过期</span>`;
  }
  return `<span class="pill warn">未下载</span>`;
}

function aria2StatusClass(status) {
  switch (status) {
    case "complete":
      return "";
    case "error":
    case "removed":
      return "bad";
    default:
      return "warn";
  }
}

function setKVSort(field) {
  if (state.kvSort.field === field) {
    state.kvSort.dir = state.kvSort.dir === "asc" ? "desc" : "asc";
  } else {
    state.kvSort.field = field;
    state.kvSort.dir = isTimeField(field) ? "desc" : "asc";
  }
  renderKVTable();
}

function renderSortButtons() {
  document.querySelectorAll("#kv-table .sort-button").forEach((button) => {
    const active = button.dataset.sort === state.kvSort.field;
    button.classList.toggle("active", active);
    button.dataset.symbol = active ? (state.kvSort.dir === "asc" ? "↑" : "↓") : "";
  });
}

function sortedKVItems() {
  const items = [...state.kvItems];
  const direction = state.kvSort.dir === "asc" ? 1 : -1;
  items.sort((a, b) => compareKV(a, b, state.kvSort.field) * direction);
  return items;
}

function compareKV(a, b, field) {
  if (isTimeField(field)) {
    const av = timeValue(a, field);
    const bv = timeValue(b, field);
    if (av === bv) return 0;
    return av < bv ? -1 : 1;
  }
  if (field === "downloaded") {
    return Number(Boolean(a.downloaded)) - Number(Boolean(b.downloaded));
  }
  return collator.compare(String(sortValue(a, field) || ""), String(sortValue(b, field) || ""));
}

function sortValue(item, field) {
  switch (field) {
    case "name":
      return item.file_name || item.id;
    case "url":
      return item.url;
    case "status":
      return item.status || "";
    default:
      return item[field];
  }
}

function isTimeField(field) {
  return field === "created_at" || field === "expires_at";
}

function timeValue(item, field) {
  if (field === "expires_at" && item.permanent) {
    return Number.POSITIVE_INFINITY;
  }
  const value = item[field];
  if (!value) return 0;
  const time = new Date(value).getTime();
  return Number.isNaN(time) ? 0 : time;
}

function updateKVSelectionState() {
  const visible = sortedKVItems();
  const selectedVisible = visible.filter((item) => state.selectedKV.has(item.id)).length;
  const selectAll = document.getElementById("select-all-kv");
  selectAll.checked = visible.length > 0 && selectedVisible === visible.length;
  selectAll.indeterminate = selectedVisible > 0 && selectedVisible < visible.length;

  const count = state.selectedKV.size;
  const downloadText = state.downloaderMode === "internal" ? "加入下载队列" : "发送到 aria2";
  document.getElementById("download-selected").textContent = count ? `${downloadText} (${count})` : downloadText;
  document.getElementById("delete-selected").textContent = count ? `批量删除 (${count})` : "批量删除";
}

async function runKVAction(action, ids, options = {}) {
  const uniqueIDs = Array.from(new Set(ids)).filter(Boolean);
  if (!uniqueIDs.length) {
    setKVStatus("请先选择需要处理的链接。", "error");
    return;
  }
  if (action === "delete" && options.confirm !== false) {
    const suffix = state.downloaderMode === "internal" ? "关联的内部下载任务会一并移除。" : "此操作不会删除 aria2 中已存在的下载任务。";
    if (!confirm(`确认删除 ${uniqueIDs.length} 条链接记录？${suffix}`)) return;
  }

  const pendingText = state.downloaderMode === "internal" ? "正在加入内部下载队列..." : "正在将链接提交到 aria2...";
  setKVStatus(action === "download" ? pendingText : "正在删除链接记录...");
  try {
    const data = await api("/api/kv/links/actions", {
      method: "POST",
      body: JSON.stringify({ action, ids: uniqueIDs }),
    });
    const message = kvActionMessage(action, data);
    const kind = data.errors && data.errors.length ? "error" : "success";
    if (action === "delete") {
      uniqueIDs.forEach((id) => state.selectedKV.delete(id));
    }
    await loadKV();
    setKVStatus(message, kind);
  } catch (error) {
    setKVStatus(error.message, "error");
  }
}

function kvActionMessage(action, data) {
  const errors = data.errors && data.errors.length ? `；失败 ${data.errors.length} 项：${data.errors.join("；")}` : "";
  if (action === "download") {
    const target = state.downloaderMode === "internal" ? "内部下载队列" : "aria2 下载队列";
    return `已将 ${data.added || 0} 条链接提交到 ${target}，跳过 ${data.skipped || 0} 条${errors}`;
  }
  return `已删除 ${data.deleted || 0} 条 KV 记录${errors}`;
}

function setKVStatus(message, kind = "") {
  const status = document.getElementById("kv-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

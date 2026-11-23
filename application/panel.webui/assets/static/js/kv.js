// KV view: download-link records that can be re-submitted to the downloader.
import { state, trackedExecutor } from "./state.js";
import { api } from "./api.js";
import { collator, escapeHTML, escapeAttr, formatBytes, formatTime } from "./utils.js";
import { statusClass, statusLabel } from "./download-model.js";
import { loadStatus } from "./status.js";
import { element, openDrawer } from "./ui.js";
import { cleanStorage } from "./storage-maintenance.js";

const $ = (id) => document.getElementById(id);
const downloadTarget = () => trackedExecutor(state.downloader);

export function initKV() {
  $("kv-search").addEventListener("input", () => {
    state.selectedKV.clear();
    setKVStatus("");
    renderKVTable();
  });
  $("refresh-kv").addEventListener("click", loadKV);
  const cleanButton = $("clean-storage");
  cleanButton.addEventListener("click", () => cleanStorage(cleanButton, {
    refresh: loadKV,
    notify: setKVStatus,
  }));

  $("select-all-kv").addEventListener("change", (event) => {
    if (event.target.checked) {
      sortedKVItems().forEach((item) => state.selectedKV.add(item.id));
    } else {
      state.selectedKV.clear();
    }
    renderKVTable();
  });

  $("select-undownloaded").addEventListener("click", () => {
    state.selectedKV.clear();
    sortedKVItems().forEach((item) => {
      if (!item.downloaded) {
        state.selectedKV.add(item.id);
      }
    });
    renderKVTable();
  });

  $("download-selected").addEventListener("click", () => {
    runKVAction("download", Array.from(state.selectedKV));
  });
  $("delete-selected").addEventListener("click", () => {
    runKVAction("delete", Array.from(state.selectedKV));
  });

  $("kv-sort").addEventListener("change", (event) => {
    const field = event.target.value;
    state.kvSort = { field, dir: isTimeField(field) ? "desc" : "asc" };
    renderKVTable();
  });
  $("kv-sort-direction").addEventListener("click", () => {
    state.kvSort.dir = state.kvSort.dir === "asc" ? "desc" : "asc";
    renderKVTable();
  });

  $("kv-body").addEventListener("change", (event) => {
    const checkbox = event.target.closest("[data-kv-check]");
    if (!checkbox) return;
    if (checkbox.checked) {
      state.selectedKV.add(checkbox.dataset.kvCheck);
    } else {
      state.selectedKV.delete(checkbox.dataset.kvCheck);
    }
    updateKVSelectionState();
  });

  $("kv-body").addEventListener("click", (event) => {
    if (event.target.closest("[data-clear-kv-search]")) {
      $("kv-search").value = "";
      $("kv-search").focus();
      renderKVTable();
      return;
    }
    const detail = event.target.closest("[data-link-details]");
    if (detail) {
      const item = state.kvItems.find((item) => item.id === detail.dataset.linkDetails);
      if (item) showKVDetails(item);
      return;
    }
    const downloadButton = event.target.closest("[data-download-link]");
    if (downloadButton) void runKVAction("download", [downloadButton.dataset.downloadLink]);
  });

  bindColumnResizing();
  renderSortButtons();
}

function showKVDetails(item) {
  const content = element("div", null, "kv-detail");
  const target = downloadTarget();
  const transfers = target === "local"
    ? `<h3>本地下载记录</h3>${renderInternalEntries(item)}`
    : target === "aria2" ? `<h3>aria2 下载记录</h3>${renderAria2Entries(item)}` : "";
  content.innerHTML = `<h3>${escapeHTML(item.file_name || item.id)}</h3>
    <dl class="detail-list">
      <dt>记录 ID</dt><dd class="mono">${escapeHTML(item.id)}</dd>
      <dt>下载地址</dt><dd class="mono"><a href="${escapeAttr(item.url)}" target="_blank" rel="noreferrer">${escapeHTML(item.url)}</a></dd>
      <dt>创建时间</dt><dd>${escapeHTML(formatTime(item.created_at))}</dd>
      <dt>有效期</dt><dd>${escapeHTML(expiryLabel(item))}</dd>
      <dt>下载状态</dt><dd>${renderDownloadedState(item)}</dd>
    </dl>${transfers}`;
  const actions = element("div", null, "download-detail-actions");
  const remove = element("button", "删除记录", "btn danger");
  remove.type = "button";
  actions.append(remove);
  content.append(actions);
  const dialog = openDrawer("链接记录详情", content);
  remove.addEventListener("click", async () => {
    if (!confirm(`删除链接记录 ${item.file_name || item.id}？${deletionEffect()}`)) return;
    dialog.close();
    try {
      await api(`/api/kv/links/${encodeURIComponent(item.id)}`, { method: "DELETE" });
      state.selectedKV.delete(item.id);
      await loadKV();
      setKVStatus("已删除链接记录。", "success");
    } catch (error) {
      setKVStatus(error.message, "error");
    }
  });
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
  const body = $("kv-body");
  setKVStatus("");
  if (!state.kvItems.length) body.innerHTML = emptyRow("正在加载链接…");
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
    state.selectedKV.clear();
    setKVStatus(error.message, "error");
    body.innerHTML = emptyRow("<strong>链接记录读取失败</strong><p>可刷新列表重试。</p>");
    updateKVSelectionState();
  }
}

function emptyRow(message) {
  return `<tr role="row"><td colspan="5" class="empty" role="cell">${message}</td></tr>`;
}

function renderKVTable() {
  const body = $("kv-body");
  const focus = document.activeElement?.closest("#kv-body :is(input, button, a)");
  const items = sortedKVItems();
  renderSortButtons();
  body.innerHTML = items.length ? items.map(renderKVRow).join("") : emptyRow(
    $("kv-search").value?.trim()
      ? '<strong>没有匹配的链接</strong><p><button class="btn secondary compact" type="button" data-clear-kv-search>清除搜索</button></p>'
      : '<strong>暂无链接记录</strong><p>在 Telegram 中生成的下载链接会显示在这里。</p>',
  );
  updateKVSelectionState(items);
  if (focus && !focus.isConnected) {
    const field = ["kvCheck", "linkDetails", "downloadLink", "kvOpen"]
      .find((name) => Object.hasOwn(focus.dataset, name));
    const replacement = field && [...body.querySelectorAll("input, button, a")]
      .find((node) => node.dataset[field] === focus.dataset[field]);
    (replacement || $("kv-search")).focus({ preventScroll: true });
  }
}

function renderKVRow(item) {
  const expires = expiryLabel(item);
  const selected = state.selectedKV.has(item.id) ? "checked" : "";
  const id = escapeAttr(item.id);
  const name = item.file_name || item.id;
  const target = downloadTarget();
  const action = target
    ? `<button class="btn secondary compact" type="button" data-download-link="${id}">${target === "local" ? "加入队列" : "发送到 aria2"}</button>`
    : `<a class="btn secondary compact" data-kv-open="${id}" href="${escapeAttr(item.url)}" target="_blank" rel="noreferrer">打开下载链接</a>`;
  return `
    <tr role="row">
      <td class="select-col" role="cell"><label class="task-check">
        <input type="checkbox" data-kv-check="${id}" aria-label="选择 ${escapeAttr(name)}" ${selected}>
      </label></td>
      <td class="kv-file" role="cell"><button class="text-button" type="button" data-link-details="${id}" aria-label="查看链接详情：${escapeAttr(name)}"><span class="download-file-label">${escapeHTML(name)}</span></button>
        <span class="subtle">${formatBytes(item.file_size || 0)}</span></td>
      <td class="kv-state" role="cell">${renderDownloadedState(item)}</td>
      <td class="kv-expiry" role="cell"><span class="task-mobile-label">有效期 </span><span class="${item.expired ? "bad-text" : ""}">${escapeHTML(expires)}</span></td>
      <td class="kv-action" role="cell">${action}</td>
    </tr>
  `;
}

function expiryLabel(item) {
  return item.permanent ? "永久" : item.expires_at ? formatTime(item.expires_at) : "—";
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
  const entries = item.local || [];
  if (!entries.length) {
    return `<span class="pill warn">尚未加入</span>`;
  }
  return entries.map((entry) => {
    const status = entry.status || "queued";
    const progress = entry.total ? ` ${formatBytes(entry.completed)} / ${formatBytes(entry.total)}` : "";
    const error = entry.error ? `<div class="subtle bad-text">${escapeHTML(entry.error)}</div>` : "";
    return `
      <div class="aria2-entry">
        <div class="mono">${escapeHTML(entry.id || "-")} <span class="pill ${statusClass(status)}">${escapeHTML(statusLabel(status))}</span></div>
        <div class="subtle">${escapeHTML(progress)}</div>
        ${error}
      </div>
    `;
  }).join("");
}

function renderDownloadedState(item) {
  if (item.http_downloaded) {
    const completedAt = item.http_downloaded_at ? `<div class="subtle">HTTP 完整传输于 ${escapeHTML(formatTime(item.http_downloaded_at))}</div>` : "";
    return `<span class="pill">已下载（HTTP）</span>${completedAt}`;
  }
  if (item.downloaded) {
    return `<span class="pill">已下载</span>`;
  }
  if (Number(item.http_delivered_bytes || 0) > 0) {
    return `<span class="pill warn">HTTP 已发送部分</span><div class="subtle">${escapeHTML(formatBytes(item.http_delivered_bytes))} / ${escapeHTML(formatBytes(item.file_size || 0))}</div>`;
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

function renderSortButtons() {
  const ascending = state.kvSort.dir === "asc";
  $("kv-sort").value = state.kvSort.field;
  const direction = $("kv-sort-direction");
  direction.textContent = ascending ? "升序" : "降序";
  direction.setAttribute("aria-label", ascending ? "当前升序，切换为降序" : "当前降序，切换为升序");
  document.querySelectorAll("#kv-table [data-kv-sort-header]").forEach((header) => {
    if (header.dataset.kvSortHeader === state.kvSort.field)
      header.setAttribute("aria-sort", ascending ? "ascending" : "descending");
    else header.removeAttribute("aria-sort");
  });
}

function sortedKVItems() {
  const query = ($("kv-search").value || "").trim().toLocaleLowerCase();
  const items = state.kvItems.filter(item => `${item.file_name} ${item.id} ${item.url}`.toLocaleLowerCase().includes(query));
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

function updateKVSelectionState(visible = sortedKVItems()) {
  const selectedVisible = visible.filter((item) => state.selectedKV.has(item.id)).length;
  const selectAll = $("select-all-kv");
  selectAll.checked = visible.length > 0 && selectedVisible === visible.length;
  selectAll.indeterminate = selectedVisible > 0 && selectedVisible < visible.length;

  const count = state.selectedKV.size;
  const target = downloadTarget();
  if (!count && document.activeElement?.closest("#kv-bulk"))
    (visible.length ? selectAll : $("kv-search")).focus({ preventScroll: true });
  $("kv-result-count").textContent = `${visible.length} 条链接记录`;
  $("select-undownloaded").disabled = !visible.some((item) => !item.downloaded);
  $("kv-bulk").hidden = count === 0;
  $("kv-selection-count").textContent = `已选 ${count} 项`;
  $("download-selected").hidden = count === 0 || !target;
  $("download-selected").textContent = target === "local" ? "加入下载队列" : "发送到 aria2";
  $("delete-selected").hidden = count === 0;
}

function deletionEffect() {
  return downloadTarget() === "local"
    ? "关联的本地下载任务会一并移除。"
    : "";
}

async function runKVAction(action, ids) {
  const target = downloadTarget();
  if (action === "download" && !target) return;
  const uniqueIDs = Array.from(new Set(ids)).filter(Boolean);
  if (!uniqueIDs.length) {
    setKVStatus("请先选择需要处理的链接。", "error");
    return;
  }
  if (action === "delete" && !confirm(`删除 ${uniqueIDs.length} 条链接记录？${deletionEffect()}`)) return;

  const pendingText = target === "local" ? "正在加入本地下载队列…" : "正在将链接提交到 aria2…";
  setKVStatus(action === "download" ? pendingText : "正在删除链接记录...");
  try {
    const data = await api("/api/kv/links/actions", {
      method: "POST",
      body: JSON.stringify({ action, ids: uniqueIDs }),
    });
    const message = kvActionMessage(action, data, target);
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

function kvActionMessage(action, data, target) {
  const errors = data.errors && data.errors.length ? `；失败 ${data.errors.length} 项：${data.errors.join("；")}` : "";
  if (action === "download") {
    const queue = target === "local" ? "本地下载队列" : "aria2 下载队列";
    return `已将 ${data.added || 0} 条链接提交到 ${queue}，跳过 ${data.skipped || 0} 条${errors}`;
  }
  return `已删除 ${data.deleted || 0} 条链接记录${errors}`;
}

function setKVStatus(message, kind = "") {
  const status = $("kv-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

export const page = { init: initKV, load: loadKV };

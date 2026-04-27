const state = {
  config: null,
  aria2Loaded: false,
  kvItems: [],
  selectedKV: new Set(),
  kvSort: { field: "created_at", dir: "desc" },
  loginPoll: null,
  update: null,
};

const collator = new Intl.Collator("zh-Hans-CN", {
  numeric: true,
  sensitivity: "base",
});

const sections = [
  {
    title: "基础",
    fields: [
      ["storage.type", "KV 存储类型", "select", "bolt/file/legacy。通常保持 bolt。", ["bolt", "file", "legacy"]],
      ["storage.path", "KV 存储路径", "text", "为空时使用默认 .tdl 数据目录。修改后建议重启。"],
      ["proxy", "代理地址", "text", "Telegram 连接代理，例如 http://127.0.0.1:10808。"],
      ["namespace", "命名空间", "text", "隔离登录态、链接和状态数据。修改后需要重启。"],
      ["debug", "调试日志", "bool", "开启更详细日志。"],
      ["threads", "单文件并发", "number", "同一个文件最多同时使用的 Telegram 抓取 worker 数。"],
      ["limit", "最大同时下载文件数", "number", "TDL HTTP 服务端同时对外提供的文件数上限。"],
      ["pool_size", "DC 下载池大小", "number", "Telegram DC client 池大小。"],
      ["delay", "DC 下载延迟", "number", "下载延迟，单位秒。"],
      ["ntp", "NTP 地址", "text", "为空时使用系统时间。"],
      ["reconnect_timeout", "重连超时", "number", "Telegram client 重连退避时间，单位秒。"],
      ["download_dir", "下载目录模板", "text", "支持 G/I/Y/M/D，/ 或 \\ 分层，& 连接同层。"],
      ["trigger_reactions", "触发表情", "list", "逗号或换行分隔；为空表示任意表情触发。"],
      ["include", "包含扩展名", "list", "只下载这些扩展名，和 exclude 互斥。"],
      ["exclude", "排除扩展名", "list", "跳过这些扩展名，和 include 互斥。"],
    ],
  },
  {
    title: "HTTP 下载代理",
    fields: [
      ["http.listen", "监听地址", "text", "TDL 下载代理监听地址，例如 0.0.0.0:22334。修改后需重启 watch。"],
      ["http.public_base_url", "公开基础地址", "text", "aria2 访问 TDL 下载代理使用的地址。"],
      ["http.download_link_ttl_hours", "链接有效期", "number", "单位小时；0 表示永久有效且不自动清理。"],
      ["http.buffer.mode", "缓冲模式", "select", "memory 使用共享内存预读；off 使用顺序流式。", ["memory", "off"]],
      ["http.buffer.size_mb", "缓冲大小", "number", "memory 模式下每个活跃文件的缓存上限，单位 MiB。"],
    ],
  },
  {
    title: "Web 管理面板",
    fields: [
      ["webui.listen", "监听地址", "text", "管理面板监听地址，例如 127.0.0.1:22335。修改后需要重启。"],
      ["webui.username", "用户名", "text", "Basic Auth 用户名。"],
      ["webui.password", "密码", "password", "Basic Auth 密码；留空表示保持原密码。"],
    ],
  },
  {
    title: "aria2",
    fields: [
      ["aria2.rpc_url", "RPC 地址", "text", "aria2 JSON-RPC 地址，管理面板会自动通过服务端代理连接。"],
      ["aria2.secret", "RPC 密钥", "password", "aria2 RPC secret；留空表示保持原密钥。"],
      ["aria2.dir", "下载根目录", "text", "为空时读取 aria2 全局 dir。"],
      ["aria2.timeout_seconds", "RPC 超时", "number", "aria2 RPC 请求超时，单位秒。"],
    ],
  },
  {
    title: "机器人",
    fields: [
      ["bot.token", "Bot Token", "password", "Telegram bot token；留空表示保持原 token。修改后需要重启。"],
      ["bot.allowed_users", "允许用户 ID", "intList", "逗号或换行分隔的 Telegram 用户 ID。"],
    ],
  },
];

document.addEventListener("DOMContentLoaded", () => {
  bindNavigation();
  bindActions();
  bindKVTable();
  bindColumnResizing();
  bindUserTabs();
  bindLoginActions();
  loadStatus();
  loadAria2();
  loadKV();
  loadUser();
  loadLoginStatus();
  loadConfig();
  loadUpdateStatus();
});

function bindNavigation() {
  document.querySelectorAll(".nav-item").forEach((button) => {
    button.addEventListener("click", () => {
      const view = button.dataset.view;
      document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item === button));
      document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
      if (view === "downloads") loadAria2();
      if (view === "kv") loadKV();
      if (view === "user") {
        loadUser();
        loadLoginStatus();
      }
      if (view === "config") loadConfig();
      if (view === "update") loadUpdateStatus();
    });
  });
}

function bindActions() {
  document.getElementById("reload-aria2").addEventListener("click", () => loadAria2(true));
  document.getElementById("refresh-kv").addEventListener("click", loadKV);
  document.getElementById("refresh-user").addEventListener("click", loadUser);
  document.getElementById("reload-config").addEventListener("click", loadConfig);
  document.getElementById("save-config").addEventListener("click", saveConfig);
  document.getElementById("reboot").addEventListener("click", reboot);
  document.getElementById("check-update").addEventListener("click", loadUpdateStatus);
  document.getElementById("apply-update").addEventListener("click", applyUpdate);
}

function bindKVTable() {
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
      if (!confirm(`删除链接记录 ${id}？此操作只清理 KV 记录，不会删除 aria2 中已存在的下载任务。`)) return;
      await api(`/api/kv/links/${encodeURIComponent(id)}`, { method: "DELETE" });
      state.selectedKV.delete(id);
      await loadKV();
    }
  });

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

function bindUserTabs() {
  document.querySelectorAll("[data-user-tab]").forEach((button) => {
    button.addEventListener("click", () => {
      const tab = button.dataset.userTab;
      document.querySelectorAll("[data-user-tab]").forEach((item) => item.classList.toggle("active", item === button));
      document.querySelectorAll(".user-tab").forEach((item) => item.classList.toggle("active", item.id === `user-tab-${tab}`));
      if (tab === "current") loadUser();
      if (tab === "login") loadLoginStatus();
    });
  });
}

function bindLoginActions() {
  document.getElementById("start-qr-login").addEventListener("click", startQRLogin);
  document.getElementById("start-phone-login").addEventListener("click", startPhoneLogin);
  document.getElementById("submit-login-code").addEventListener("click", submitLoginCode);
  document.getElementById("submit-login-password").addEventListener("click", submitLoginPassword);
  document.getElementById("cancel-login").addEventListener("click", cancelLogin);
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text.trim() };
    }
  }
  if (!response.ok) {
    throw new Error(data.error || data.message || response.statusText);
  }
  return data;
}

async function loadStatus() {
  try {
    const data = await api("/api/status");
    document.getElementById("runtime-namespace").textContent = `namespace: ${data.namespace || "-"}`;
    document.getElementById("runtime-watch").textContent = `watch: ${data.watch_running ? "running" : "stopped"}`;
  } catch (error) {
    document.getElementById("runtime-watch").textContent = `status: ${error.message}`;
  }
}

function loadAria2(force = false) {
  if (state.aria2Loaded && !force) return;
  const protocol = location.protocol.replace(":", "");
  const port = location.port || (protocol === "https" ? "443" : "80");
  const query = new URLSearchParams({
    protocol,
    host: location.hostname,
    port,
    interface: "aria2/jsonrpc",
  });
  document.getElementById("aria2-frame").src = `/aria2ng.html#!/settings/rpc/set?${query.toString()}`;
  state.aria2Loaded = true;
}

async function loadKV() {
  const body = document.getElementById("kv-body");
  setKVStatus("");
  body.innerHTML = `<tr><td colspan="8" class="empty">正在加载...</td></tr>`;
  try {
    const data = await api("/api/kv/links");
    if (data.status_error) {
      setKVStatus(`aria2 状态查询失败：${data.status_error}`, "error");
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
      <td>${renderAria2Entries(item)}</td>
      <td>${renderDownloadedState(item)}</td>
      <td class="time-cell">${formatTime(item.created_at)}</td>
      <td class="time-cell">${escapeHTML(expires)}</td>
      <td>
        <div class="row-actions">
          <button class="btn primary compact" data-download-link="${escapeAttr(item.id)}">发送到 aria2</button>
          <button class="btn danger compact" data-delete-link="${escapeAttr(item.id)}">删除</button>
        </div>
      </td>
    </tr>
  `;
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
  document.getElementById("download-selected").textContent = count ? `发送到 aria2 (${count})` : "发送到 aria2";
  document.getElementById("delete-selected").textContent = count ? `批量删除 (${count})` : "批量删除";
}

async function runKVAction(action, ids, options = {}) {
  const uniqueIDs = Array.from(new Set(ids)).filter(Boolean);
  if (!uniqueIDs.length) {
    setKVStatus("请先选择需要处理的链接。", "error");
    return;
  }
  if (action === "delete" && options.confirm !== false) {
    if (!confirm(`确认删除 ${uniqueIDs.length} 条链接记录？此操作不会删除 aria2 中已存在的下载任务。`)) return;
  }

  setKVStatus(action === "download" ? "正在将链接提交到 aria2..." : "正在删除链接记录...");
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
    return `已将 ${data.added || 0} 条链接提交到 aria2 下载队列，跳过 ${data.skipped || 0} 条${errors}`;
  }
  return `已删除 ${data.deleted || 0} 条 KV 记录${errors}`;
}

function setKVStatus(message, kind = "") {
  const status = document.getElementById("kv-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

async function loadUser() {
  const target = document.getElementById("user-info");
  target.innerHTML = `<div class="info-item"><div class="info-value">正在检查...</div></div>`;
  try {
    const data = await api("/api/user");
    const user = data.user || {};
    const rows = [
      ["登录状态", data.valid ? "有效" : "无效"],
      ["状态详情", data.status || "-"],
      ["Namespace", data.namespace || "-"],
      ["Watch", data.watch_running ? "运行中" : "未运行"],
      ["用户 ID", user.id || "-"],
      ["用户名", user.username || "-"],
      ["姓名", user.name || "-"],
      ["手机号", user.phone || "-"],
      ["允许用户", (data.allowed_users || []).join(", ") || "-"],
    ];
    target.innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
  } catch (error) {
    target.innerHTML = infoItem("检查失败", error.message);
  }
}

function infoItem(label, value) {
  return `
    <div class="info-item">
      <div class="info-label">${escapeHTML(label)}</div>
      <div class="info-value">${escapeHTML(String(value))}</div>
    </div>
  `;
}

async function loadLoginStatus() {
  try {
    const data = await api("/api/login/status");
    renderLoginStatus(data);
    if (data.active) {
      startLoginPolling();
    } else {
      stopLoginPolling();
    }
  } catch (error) {
    renderLoginError(error.message);
  }
}

async function startQRLogin() {
  await loginRequest("/api/login/qr/start", {});
}

async function startPhoneLogin() {
  const phone = document.getElementById("login-phone").value.trim();
  if (!phone) {
    renderLoginError("请输入手机号。");
    return;
  }
  await loginRequest("/api/login/phone/start", { phone });
}

async function submitLoginCode() {
  const code = document.getElementById("login-code").value.trim();
  if (!code) {
    renderLoginError("请输入 Telegram 收到的原始验证码。");
    return;
  }
  await loginRequest("/api/login/code", { code });
}

async function submitLoginPassword() {
  const password = document.getElementById("login-password").value;
  if (!password) {
    renderLoginError("请输入 Telegram 2FA 密码。");
    return;
  }
  await loginRequest("/api/login/password", { password });
}

async function cancelLogin() {
  try {
    await api("/api/login/cancel", { method: "POST", body: "{}" });
    stopLoginPolling();
    await loadLoginStatus();
  } catch (error) {
    renderLoginError(error.message);
  }
}

async function loginRequest(path, body) {
  setLoginStatus("正在处理登录请求...");
  try {
    const data = await api(path, {
      method: "POST",
      body: JSON.stringify(body || {}),
    });
    renderLoginStatus(data);
    if (data.active) {
      startLoginPolling();
    } else {
      stopLoginPolling();
    }
  } catch (error) {
    renderLoginError(error.message);
  }
}

function startLoginPolling() {
  if (state.loginPoll) return;
  state.loginPoll = window.setInterval(async () => {
    try {
      const data = await api("/api/login/status");
      renderLoginStatus(data);
      if (!data.active) {
        stopLoginPolling();
        if (data.stage === "done") {
          loadUser();
          loadStatus();
        }
      }
    } catch (error) {
      renderLoginError(error.message);
      stopLoginPolling();
    }
  }, 2000);
}

function stopLoginPolling() {
  if (!state.loginPoll) return;
  window.clearInterval(state.loginPoll);
  state.loginPoll = null;
}

function renderLoginStatus(data) {
  const parts = [];
  if (data.kind) parts.push(`方式：${data.kind === "qr" ? "二维码登录" : "手机号登录"}`);
  if (data.phone) parts.push(`手机号：${data.phone}`);
  if (data.status) parts.push(data.status);
  if (data.error) parts.push(`错误：${data.error}`);
  if (data.user) parts.push(`用户：${data.user.name || data.user.username || data.user.id || "-"}`);

  const kind = data.error || data.stage === "failed" ? "error" : data.stage === "done" ? "success" : "";
  setLoginStatus(parts.join("\n") || "当前没有登录流程。", kind);
  renderQRCode(data);
}

function renderLoginError(message) {
  setLoginStatus(message, "error");
}

function setLoginStatus(message, kind = "") {
  const status = document.getElementById("login-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

function renderQRCode(data) {
  const box = document.getElementById("qr-box");
  if (data.qr_image) {
    box.innerHTML = `<img src="${escapeAttr(data.qr_image)}" alt="Telegram 登录二维码">`;
    return;
  }
  if (data.kind === "qr" && data.active) {
    box.innerHTML = `<div class="empty compact-empty">正在等待二维码...</div>`;
    return;
  }
  box.innerHTML = "";
}

async function loadUpdateStatus() {
  const status = document.getElementById("update-status");
  const target = document.getElementById("update-info");
  const notes = document.getElementById("update-notes");
  status.className = "notice";
  status.textContent = "正在检查更新...";
  target.innerHTML = "";
  notes.textContent = "";
  try {
    const data = await api("/api/update/check");
    state.update = data.update;
    renderUpdateInfo(data.update);
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function renderUpdateInfo(update) {
  const status = document.getElementById("update-status");
  const target = document.getElementById("update-info");
  const notes = document.getElementById("update-notes");
  if (!update) {
    status.className = "notice";
    status.textContent = "";
    target.innerHTML = "";
    notes.textContent = "";
    return;
  }
  const rows = [
    ["当前版本", update.current_version || "-"],
    ["当前提交", update.current_commit || "-"],
    ["构建日期", update.current_date || "-"],
    ["运行平台", `${update.goos || "-"} / ${update.goarch || "-"}`],
    ["最新版本", update.latest_version || "-"],
    ["发布名称", update.latest_name || "-"],
    ["更新资产", update.asset_name || "-"],
    ["发布地址", update.latest_url || "-"],
  ];
  target.innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
  notes.textContent = update.release_notes || "";
  const kind = update.needs_update ? "success" : "";
  status.className = `notice ${kind}`.trim();
  status.textContent = update.message || (update.needs_update ? "发现新版本。" : "当前已是最新版本。");
  document.getElementById("apply-update").disabled = !update.needs_update || !update.can_update;
}

async function applyUpdate() {
  if (!state.update) {
    await loadUpdateStatus();
  }
  if (!state.update || !state.update.needs_update || !state.update.can_update) {
    return;
  }
  if (!confirm(`确认更新到 ${state.update.latest_version}？程序会自动重启。`)) return;
  const status = document.getElementById("update-status");
  status.className = "notice";
  status.textContent = "正在下载更新...";
  try {
    const data = await api("/api/update/apply", { method: "POST", body: "{}" });
    if (data.update) {
      state.update = data.update;
      renderUpdateInfo(data.update);
    }
    status.className = "notice success";
    status.textContent = data.message || "更新包已下载，正在重启。";
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

async function loadConfig() {
  const status = document.getElementById("config-status");
  status.className = "notice";
  status.textContent = "";
  try {
    const data = await api("/api/config");
    state.config = data.config;
    renderConfigForm();
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function renderConfigForm() {
  const form = document.getElementById("config-form");
  form.innerHTML = sections.map((section) => `
    <section class="config-section">
      <h2>${escapeHTML(section.title)}</h2>
      <div class="field-grid">
        ${section.fields.map(renderField).join("")}
      </div>
    </section>
  `).join("");
}

function renderField(field) {
  const [path, label, type, help, options] = field;
  const value = getPath(state.config, path);
  let control = "";
  if (type === "select") {
    control = `<select data-path="${escapeAttr(path)}" data-type="${type}">
      ${(options || []).map((option) => `<option value="${escapeAttr(option)}" ${String(value) === option ? "selected" : ""}>${escapeHTML(option)}</option>`).join("")}
    </select>`;
  } else if (type === "bool") {
    control = `<label class="checkbox-line"><input type="checkbox" data-path="${escapeAttr(path)}" data-type="${type}" ${value ? "checked" : ""}> 启用</label>`;
  } else if (type === "list" || type === "intList") {
    control = `<textarea data-path="${escapeAttr(path)}" data-type="${type}">${escapeHTML((value || []).join(", "))}</textarea>`;
  } else if (type === "password") {
    control = `<input type="password" data-path="${escapeAttr(path)}" data-type="${type}" value="" placeholder="留空保持不变">`;
  } else {
    control = `<input type="${type === "number" ? "number" : "text"}" data-path="${escapeAttr(path)}" data-type="${type}" value="${escapeAttr(value ?? "")}">`;
  }
  return `
    <div class="field">
      <label>${escapeHTML(label)}</label>
      ${control}
      <small>${escapeHTML(help || path)}</small>
    </div>
  `;
}

async function saveConfig(event) {
  event.preventDefault();
  const status = document.getElementById("config-status");
  status.className = "notice";
  status.textContent = "正在保存...";
  const values = {};
  document.querySelectorAll("#config-form [data-path]").forEach((input) => {
    const path = input.dataset.path;
    const type = input.dataset.type;
    if (type === "password" && !input.value) return;
    values[path] = fieldValue(input, type);
  });
  try {
    const data = await api("/api/config", {
      method: "PATCH",
      body: JSON.stringify({ values }),
    });
    state.config = data.config;
    renderConfigForm();
    status.className = "notice success";
    status.textContent = data.message || "配置已保存";
    loadStatus();
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function fieldValue(input, type) {
  if (type === "bool") return input.checked;
  if (type === "number") return Number(input.value || 0);
  if (type === "list") return splitList(input.value);
  if (type === "intList") return splitList(input.value).map((value) => Number(value)).filter((value) => Number.isFinite(value));
  return input.value;
}

async function reboot() {
  if (!confirm("确认重启 tdl？当前 Web 连接会暂时断开。")) return;
  const status = document.getElementById("config-status");
  try {
    const data = await api("/api/system/reboot", { method: "POST", body: "{}" });
    status.className = "notice success";
    status.textContent = data.message || "正在重启";
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function getPath(obj, path) {
  return path.split(".").reduce((current, part) => current && current[part], obj);
}

function splitList(value) {
  return String(value || "").split(/[\s,]+/).map((item) => item.trim()).filter(Boolean);
}

function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

function formatBytes(value) {
  let size = Number(value || 0);
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

function escapeAttr(value) {
  return escapeHTML(value).replace(/`/g, "&#96;");
}

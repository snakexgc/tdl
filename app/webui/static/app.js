const state = {
  config: null,
  aria2Loaded: false,
  kvItems: [],
  selectedKV: new Set(),
  kvSort: { field: "created_at", dir: "desc" },
  loginPoll: null,
  update: null,
  modules: [],
  userSessions: [],
  currentNamespace: "",
};

const collator = new Intl.Collator("zh-Hans-CN", {
  numeric: true,
  sensitivity: "base",
});

const sections = [
  {
    title: "基础",
    fields: [
      ["proxy", "代理地址", "text", "需要代理访问 Telegram 或 GitHub 时填写，例如 http://127.0.0.1:10808。"],
      ["debug", "详细日志", "bool", "排查问题时开启，平时保持关闭。"],
      ["pool_size", "下载并发", "number", "Telegram 连接池大小，也用于单个文件的分片下载并发；不确定时保持默认。"],
      ["delay", "任务间隔", "number", "两个下载任务之间等待的秒数，通常为 0。"],
      ["ntp", "时间校准服务器", "text", "系统时间不准时填写，例如 pool.ntp.org。"],
      ["reconnect_timeout", "重连等待时间", "number", "网络断开后等待多久再重连，单位秒。"],
      ["download_dir", "下载目录规则", "text", "用于按群组、日期等自动分目录，例如 G/Y&M。"],
      ["trigger_reactions", "触发表情", "list", "只监听这些表情；留空表示任意表情都可以触发。"],
      ["include", "只下载这些扩展名", "list", "例如 mp4,mkv；留空表示不限制。"],
      ["exclude", "跳过这些扩展名", "list", "例如 png,jpg；留空表示不跳过。"],
    ],
  },
  {
    title: "下载链接",
    fields: [
      ["http.listen", "监听地址", "text", "tdl 提供下载链接的地址，例如 0.0.0.0:22334。"],
      ["http.public_base_url", "对外访问地址", "text", "aria2 能访问到的 tdl 地址，不同机器时请填写局域网地址。"],
      ["http.download_link_ttl_hours", "链接保留时间", "number", "单位小时；填 0 表示永久保留。"],
      ["http.buffer.mode", "下载缓冲", "select", "memory 适合多数场景；off 表示不预读。", ["memory", "off"]],
      ["http.buffer.size_mb", "缓冲大小", "number", "每个活跃文件可使用的内存上限，单位 MiB。"],
    ],
  },
  {
    title: "Web 管理面板",
    fields: [
      ["webui.listen", "访问地址", "text", "管理面板监听地址，例如 127.0.0.1:22335。修改后需要重启。"],
      ["webui.username", "用户名", "text", "登录管理面板时使用的用户名。"],
      ["webui.password", "密码", "password", "管理面板登录密码；留空表示保持原密码。"],
    ],
  },
  {
    title: "模块开关",
    fields: [
      ["modules.bot", "机器人控制", "bool", "启用后可以通过 Telegram 私聊命令控制 tdl。"],
      ["modules.watch", "监听下载", "bool", "启用后监听 Telegram 表情，并把文件提交到 aria2。"],
    ],
  },
  {
    title: "aria2",
    fields: [
      ["aria2.rpc_url", "aria2 连接地址", "text", "aria2 的连接地址，例如 http://127.0.0.1:6800/jsonrpc。"],
      ["aria2.secret", "aria2 密钥", "password", "aria2 设置了密钥时填写；留空表示保持原密钥。"],
      ["aria2.dir", "下载根目录", "text", "aria2 保存文件的根目录；留空时使用 aria2 默认目录。"],
      ["aria2.timeout_seconds", "连接超时", "number", "连接 aria2 等待的秒数。"],
    ],
  },
  {
    title: "机器人",
    fields: [
      ["bot.token", "机器人 Token", "password", "从 BotFather 获取；留空表示保持原 token。"],
      ["bot.allowed_users", "允许用户 ID", "intList", "只有这些 Telegram 用户可以控制机器人，多个 ID 用逗号或换行分隔。"],
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
  loadModules();
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
      if (view === "modules") loadModules();
      if (view === "config") loadConfig();
      if (view === "update") loadUpdateStatus();
    });
  });
}

function bindActions() {
  document.getElementById("reload-aria2").addEventListener("click", () => loadAria2(true));
  document.getElementById("refresh-kv").addEventListener("click", loadKV);
  document.getElementById("refresh-user").addEventListener("click", loadUser);
  document.getElementById("switch-user").addEventListener("click", switchUser);
  document.getElementById("delete-user").addEventListener("click", deleteUser);
  document.getElementById("switch-namespace").addEventListener("change", () => updateUserActionButtons());
  document.getElementById("reload-config").addEventListener("click", loadConfig);
  document.getElementById("refresh-modules").addEventListener("click", loadModules);
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
    const version = data.version || {};
    document.getElementById("runtime-version").textContent = `版本：${version.version || "-"}`;
    document.getElementById("runtime-namespace").textContent = `数据空间：${data.namespace || "-"}`;
    document.getElementById("runtime-watch").textContent = `监听：${data.watch_running ? "运行中" : "未运行"}`;
  } catch (error) {
    document.getElementById("runtime-watch").textContent = `状态：${error.message}`;
  }
}

async function loadModules() {
  const target = document.getElementById("module-list");
  setModuleStatus("");
  target.innerHTML = `<div class="empty compact-empty">正在加载...</div>`;
  try {
    const data = await api("/api/modules");
    state.modules = data.modules || [];
    renderModules();
    loadStatus();
  } catch (error) {
    target.innerHTML = "";
    setModuleStatus(error.message, "error");
  }
}

function renderModules() {
  const target = document.getElementById("module-list");
  if (!state.modules.length) {
    target.innerHTML = `<div class="empty compact-empty">没有可管理的模块</div>`;
    return;
  }
  target.innerHTML = state.modules.map(renderModuleCard).join("");
  target.querySelectorAll("[data-module-toggle]").forEach((button) => {
    button.addEventListener("click", () => toggleModule(button.dataset.moduleToggle, button.dataset.nextEnabled === "true"));
  });
}

function renderModuleCard(module) {
  const running = module.running ? "运行中" : "未运行";
  const enabled = module.enabled ? "已启用" : "已关闭";
  const nextEnabled = !module.enabled;
  const toggleText = module.enabled ? "关闭" : "启用";
  const disabled = module.can_toggle ? "" : "disabled";
  return `
    <section class="module-card">
      <div class="module-main">
        <div>
          <h2>${escapeHTML(module.name || module.id)}</h2>
          <p>${escapeHTML(module.description || "")}</p>
        </div>
        <div class="module-badges">
          <span class="pill ${module.enabled ? "" : "warn"}">${enabled}</span>
          <span class="pill ${module.running ? "" : "warn"}">${running}</span>
        </div>
      </div>
      <div class="module-foot">
        <div class="module-status">${escapeHTML(module.status || "-")}</div>
        <button class="btn ${module.enabled ? "danger" : "primary"}" data-module-toggle="${escapeAttr(module.id)}" data-next-enabled="${nextEnabled}" ${disabled}>${toggleText}</button>
      </div>
    </section>
  `;
}

async function toggleModule(id, enabled) {
  setModuleStatus(enabled ? "正在启用模块..." : "正在关闭模块...");
  try {
    const data = await api("/api/modules", {
      method: "POST",
      body: JSON.stringify({ id, enabled }),
    });
    state.modules = data.modules || [];
    renderModules();
    loadStatus();
    setModuleStatus(data.module && data.module.status ? data.module.status : "模块状态已更新。", "success");
  } catch (error) {
    setModuleStatus(error.message, "error");
  }
}

function setModuleStatus(message, kind = "") {
  const status = document.getElementById("module-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
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
      ["数据空间", data.namespace || "-"],
      ["监听下载", data.watch_running ? "运行中" : "未运行"],
      ["用户 ID", user.id || "-"],
      ["用户名", user.username || "-"],
      ["姓名", user.name || "-"],
      ["手机号", user.phone || "-"],
      ["允许用户", (data.allowed_users || []).join(", ") || "-"],
    ];
    target.innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
    state.userSessions = data.sessions || [];
    state.currentNamespace = data.namespace || "";
    renderUserSwitch(data.namespace || "", state.userSessions, data.sessions_error || "");
    setNamespaceInputs(data.namespace || "");
  } catch (error) {
    target.innerHTML = infoItem("检查失败", error.message);
  }
}

function setNamespaceInputs(namespace) {
  const loginInput = document.getElementById("login-namespace");
  if (loginInput && !loginInput.value) loginInput.value = namespace || "";
}

function renderUserSwitch(currentNamespace, sessions, error = "") {
  const select = document.getElementById("switch-namespace");
  const button = document.getElementById("switch-user");
  const deleteButton = document.getElementById("delete-user");
  if (!select || !button || !deleteButton) return;

  const usable = Array.isArray(sessions) ? sessions.filter((item) => item && item.namespace) : [];
  if (!usable.length) {
    select.innerHTML = `<option value="">${escapeHTML(error || "未发现已登录用户")}</option>`;
    select.disabled = true;
    button.disabled = true;
    deleteButton.disabled = true;
    deleteButton.title = "";
    return;
  }

  select.disabled = false;
  select.innerHTML = usable.map((item) => {
    const label = item.current || item.namespace === currentNamespace ? `${item.namespace}（当前）` : item.namespace;
    return `<option value="${escapeAttr(item.namespace)}">${escapeHTML(label)}</option>`;
  }).join("");
  if (usable.some((item) => item.namespace === currentNamespace)) {
    select.value = currentNamespace;
  }
  updateUserActionButtons(currentNamespace);
}

function updateUserActionButtons(currentNamespace = state.currentNamespace) {
  const select = document.getElementById("switch-namespace");
  const switchButton = document.getElementById("switch-user");
  const deleteButton = document.getElementById("delete-user");
  if (!select || !switchButton || !deleteButton) return;

  const namespace = (select.value || "").trim();
  const hasNamespace = Boolean(namespace);
  switchButton.disabled = !hasNamespace;
  deleteButton.disabled = !hasNamespace || namespace === currentNamespace;
  deleteButton.title = namespace === currentNamespace ? "请先切换到其他用户后再删除当前用户" : "";
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
  const namespace = readNamespaceInput("login-namespace", renderLoginError);
  if (!namespace) return;
  await loginRequest("/api/login/qr/start", { namespace });
}

async function startPhoneLogin() {
  const phone = document.getElementById("login-phone").value.trim();
  if (!phone) {
    renderLoginError("请输入手机号。");
    return;
  }
  const namespace = readNamespaceInput("login-namespace", renderLoginError);
  if (!namespace) return;
  await loginRequest("/api/login/phone/start", { phone, namespace });
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
  if (data.namespace) parts.push(`用户：${data.namespace}`);
  if (data.phone) parts.push(`手机号：${data.phone}`);
  if (data.status) parts.push(data.status);
  if (data.error) parts.push(`错误：${data.error}`);
  if (data.user) parts.push(`用户：${data.user.name || data.user.username || data.user.id || "-"}`);

  const kind = data.error || data.stage === "failed" ? "error" : data.stage === "done" ? "success" : "";
  setLoginStatus(parts.join("\n") || "当前没有登录流程。", kind);
  renderQRCode(data);
}

async function switchUser() {
  const namespace = readSelectedNamespace();
  if (!namespace) return;
  if (!confirm(`切换到用户 ${namespace} 并重启 tdl？`)) return;
  try {
    const data = await api("/api/user/switch", {
      method: "POST",
      body: JSON.stringify({ namespace }),
    });
    setUserStatus(data.message || "正在切换用户。", "success");
  } catch (error) {
    setUserStatus(error.message, "error");
  }
}

async function deleteUser() {
  const namespace = readSelectedNamespace();
  if (!namespace) return;
  if (namespace === state.currentNamespace) {
    setUserStatusError("当前用户正在运行中，请先切换到其他用户后再删除。");
    return;
  }
  if (!confirm(`删除用户 ${namespace} 的登录数据？该用户将不再出现在切换列表中。`)) return;
  try {
    const data = await api("/api/user/delete", {
      method: "POST",
      body: JSON.stringify({ namespace }),
    });
    setUserStatus(data.message || "用户登录数据已删除。", "success");
    await loadUser();
  } catch (error) {
    setUserStatus(error.message, "error");
  }
}

function readSelectedNamespace() {
  const select = document.getElementById("switch-namespace");
  const namespace = (select && select.value ? select.value : "").trim();
  if (!namespace) {
    setUserStatusError("请选择一个已登录用户。");
    if (select) select.focus();
    return "";
  }
  if (!/^[A-Za-z]+$/.test(namespace)) {
    setUserStatusError("用户数据文件名无效，无法操作。");
    if (select) select.focus();
    return "";
  }
  return namespace;
}

function readNamespaceInput(id, showError) {
  const input = document.getElementById(id);
  const namespace = (input && input.value ? input.value : "").trim();
  if (!namespace) {
    showError("请先输入用户名。");
    if (input) input.focus();
    return "";
  }
  if (!/^[A-Za-z]+$/.test(namespace)) {
    showError("用户名只能使用英文字母。");
    if (input) input.focus();
    return "";
  }
  return namespace;
}

function setUserStatusError(message) {
  setUserStatus(message, "error");
}

function setUserStatus(message, kind = "") {
  const status = document.getElementById("user-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
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
    ["更新文件", update.asset_name || "-"],
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
    loadModules();
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

const state = {
  config: null,
  aria2Loaded: false,
  downloaderMode: "aria2",
  internalDownloads: [],
  selectedInternalDownloads: new Set(),
  internalDownloadSamples: new Map(),
  internalDownloadPoll: null,
  internalDownloadLoading: false,
  kvItems: [],
  selectedKV: new Set(),
  kvSort: { field: "created_at", dir: "desc" },
  loginPoll: null,
  update: null,
  modules: [],
  userSessions: [],
  currentNamespace: "",
  loginMethod: "phone",
  loginPanel: "",
  lastLoginData: null,
  heartbeatTimer: null,
  heartbeatLastSeen: 0,
  heartbeatState: "checking",
  usingDefaultCredentials: false,
};

const collator = new Intl.Collator("zh-Hans-CN", {
  numeric: true,
  sensitivity: "base",
});

const views = ["user", "config", "downloads", "kv", "modules", "update"];
const heartbeatIntervalMS = 1000;
const heartbeatOfflineMS = 3000;
const heartbeatRequestTimeoutMS = 1500;
const internalDownloadRefreshMS = 1000;
const exclusiveListPairs = [["include", "exclude"]];
const proxySchemes = ["socks5://", "socks5h://", "http://", "https://"];

const sections = [
  {
    title: "基础",
    fields: [
      ["proxy", "代理地址", "proxy", "选择代理协议后，只填写 IP 或域名加端口，例如 127.0.0.1:1080。"],
      ["proxy_username", "代理用户名", "text", "代理需要认证时填写；没有认证时留空。"],
      ["proxy_password", "代理密码", "password", "代理需要认证时填写；没有认证时留空，保存时留空表示保持原密码。"],
      ["debug", "详细日志", "bool", "排查问题时开启，平时保持关闭。"],
      ["pool_size", "下载并发", "number", "Telegram 连接池大小，也用于单个文件的分片下载并发；不确定时保持默认。"],
      ["delay", "任务间隔", "number", "两个下载任务之间等待的秒数，通常为 0。"],
      ["ntp", "时间校准服务器", "text", "留空时启动会自动选择最快的内置服务器；手动填写后会优先检测该服务器。"],
      ["reconnect_timeout", "重连等待时间", "number", "网络断开后等待多久再重连，单位秒。"],
      ["download_dir", "下载目录规则", "text", "用于按群组、日期等自动分目录，例如 G\\Y&M。"],
      ["trigger_reactions", "触发表情", "list", "只监听这些表情；留空表示任意表情都可以触发。"],
      ["include", "只下载这些扩展名", "list", "例如 mp4、mkv；留空表示不限制。"],
      ["exclude", "跳过这些扩展名", "list", "例如 png、jpg；留空表示不跳过。"],
      ["file_size_mb", "文件大小过滤", "number", "单位 MB；填 0 表示不限制，小于该大小的文件会在扩展名过滤后跳过。"],
    ],
  },
  {
    title: "下载链接",
    fields: [
      ["http.address", "监听地址", "text", "tdl 提供下载链接的监听地址，例如 0.0.0.0 或 127.0.0.1。"],
      ["http.port", "监听端口", "number", "tdl 提供下载链接的监听端口，例如 22334。"],
      ["http.public_base_url", "对外访问地址", "text", "aria2 能访问到的 tdl 地址，不同机器时请填写局域网地址。"],
      ["http.download_link_ttl_hours", "链接保留时间", "number", "单位小时；填 0 表示永久保留。"],
      ["http.buffer.mode", "下载缓冲", "select", "memory 适合多数场景；off 表示不预读。", ["memory", "off"]],
      ["http.buffer.size_mb", "缓冲大小", "number", "每个活跃文件可使用的内存上限，单位 MiB。"],
    ],
  },
  {
    title: "Web 管理面板",
    fields: [
      ["webui.address", "监听地址", "text", "管理面板监听地址，例如 0.0.0.0 或 127.0.0.1。修改后需要重启。"],
      ["webui.port", "监听端口", "number", "管理面板监听端口，例如 22335。修改后需要重启。"],
      ["webui.username", "用户名", "text", "登录管理面板时使用的用户名。"],
      ["webui.password", "密码", "password", "管理面板登录密码；留空表示保持原密码。"],
    ],
  },
  {
    title: "模块开关",
    fields: [
      ["modules.bot", "机器人控制", "bool", "启用后可以通过 Telegram 私聊命令控制 tdl。"],
      ["modules.watch", "监听下载", "bool", "启用后监听 Telegram 表情，并把文件提交到当前下载器。"],
    ],
  },
  {
    title: "下载器",
    fields: [
      ["downloader.mode", "下载器模式", "select", "aria2 使用外部 aria2；internal 使用 tdl 内部简易本地下载器，单文件线程数由 pool_size 控制、同时 1 个文件。", ["aria2", "internal"]],
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
      ["bot.allowed_users", "允许用户 ID", "intList", "只有这些 Telegram 用户可以控制机器人。"],
    ],
  },
];

document.addEventListener("DOMContentLoaded", () => {
  bootstrap().catch((error) => {
    const host = document.getElementById("view-host");
    if (host) {
      host.innerHTML = `<div class="notice error">管理面板载入失败：${escapeHTML(error.message)}</div>`;
    }
  });
});

async function bootstrap() {
  await loadViews();
  bindNavigation();
  bindActions();
  bindKVTable();
  bindColumnResizing();
  bindUserTabs();
  bindLoginActions();
  loadStatus();
  loadActiveViewData();
  startHeartbeat();
}

async function loadViews() {
  const host = document.getElementById("view-host");
  const html = await Promise.all(views.map(async (view) => {
    const response = await fetch(`/views/${view}.html`, { credentials: "same-origin" });
    if (response.status === 401) {
      window.location.href = "/login";
      throw new Error("登录已过期。");
    }
    if (!response.ok) {
      throw new Error(`无法载入 ${view}.html`);
    }
    return response.text();
  }));
  host.innerHTML = html.join("");
}

function bindNavigation() {
  document.querySelectorAll(".nav-item").forEach((button) => {
    button.addEventListener("click", () => {
      selectView(button.dataset.view);
    });
  });
}

function selectView(view) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.view === view));
  document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
  if (view !== "downloads") stopInternalDownloadPolling();
  loadViewData(view);
}

function loadActiveViewData() {
  const view = document.querySelector(".nav-item.active")?.dataset.view || "user";
  loadViewData(view);
}

function loadViewData(view) {
  if (view === "downloads") loadDownloads();
  if (view === "kv") loadKV();
  if (view === "user") {
    loadUser();
    loadLoginStatus();
  }
  if (view === "modules") loadModules();
  if (view === "config") loadConfig();
  if (view === "update") loadUpdateStatus();
}

function bindActions() {
  document.getElementById("logout").addEventListener("click", logout);
  document.getElementById("reload-aria2").addEventListener("click", () => loadDownloads(true));
  document.getElementById("aria2-retry-check").addEventListener("click", () => loadDownloads(true));
  document.getElementById("aria2-open-config").addEventListener("click", async () => {
    selectView("config");
    await loadConfig();
    requestAnimationFrame(() => {
      const input = document.querySelector('#config-form [data-path="aria2.rpc_url"]');
      if (input) {
        input.focus();
        input.scrollIntoView({ block: "center" });
      }
    });
  });
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
  const credentialWarningAction = document.getElementById("credential-warning-action");
  if (credentialWarningAction) {
    credentialWarningAction.addEventListener("click", openCredentialSettings);
  }
}

async function logout() {
  if (state.heartbeatTimer) {
    clearInterval(state.heartbeatTimer);
    state.heartbeatTimer = null;
  }
  stopInternalDownloadPolling();
  try {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
  } finally {
    window.location.href = "/login";
  }
}

function startHeartbeat() {
  state.heartbeatLastSeen = Date.now();
  setHeartbeatState("checking");
  runHeartbeat();
  state.heartbeatTimer = window.setInterval(runHeartbeat, heartbeatIntervalMS);
}

async function runHeartbeat() {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), heartbeatRequestTimeoutMS);
  try {
    const response = await fetch("/api/heartbeat", {
      credentials: "same-origin",
      signal: controller.signal,
      cache: "no-store",
    });
    if (response.status === 401) {
      window.location.href = "/login";
      return;
    }
    if (!response.ok) {
      throw new Error(response.statusText);
    }
    state.heartbeatLastSeen = Date.now();
    setHeartbeatState("online");
  } catch {
    const elapsed = state.heartbeatLastSeen ? Date.now() - state.heartbeatLastSeen : heartbeatOfflineMS;
    if (elapsed >= heartbeatOfflineMS) {
      setHeartbeatState("offline");
    }
  } finally {
    window.clearTimeout(timeout);
  }
}

function setHeartbeatState(next) {
  if (state.heartbeatState === next) return;
  state.heartbeatState = next;
  const indicator = document.getElementById("heartbeat-indicator");
  const label = document.getElementById("heartbeat-label");
  if (!indicator || !label) return;
  indicator.dataset.state = next;
  if (next === "online") {
    label.textContent = "TDL 在线";
  } else if (next === "offline") {
    label.textContent = "TDL 离线";
  } else {
    label.textContent = "TDL 连接中";
  }
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
  document.getElementById("start-phone-login").addEventListener("click", startPhoneLogin);
  document.getElementById("submit-login-code").addEventListener("click", submitLoginCode);
  document.getElementById("submit-login-password").addEventListener("click", submitLoginPassword);
  document.getElementById("cancel-login").addEventListener("click", cancelLogin);
  document.getElementById("restart-login-flow").addEventListener("click", resetLoginFlow);
  document.getElementById("finish-login-flow").addEventListener("click", finishLoginFlow);

  [
    ["login-namespace", startSelectedLogin],
    ["login-phone", startPhoneLogin],
    ["login-code", submitLoginCode],
    ["login-password", submitLoginPassword],
  ].forEach(([id, submit]) => {
    const input = document.getElementById(id);
    if (!input) return;
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      submit();
    });
  });

  updateLoginMethodUI();
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
  if (response.status === 401) {
    window.location.href = "/login";
    throw new Error("登录已过期，请重新登录。");
  }
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
    state.downloaderMode = data.downloader && data.downloader.mode ? data.downloader.mode : "aria2";
    const version = data.version || {};
    document.getElementById("runtime-version").textContent = `版本：${version.version || "-"}`;
    document.getElementById("runtime-namespace").textContent = `数据空间：${data.namespace || "-"}`;
    document.getElementById("runtime-watch").textContent = `监听：${data.watch_running ? "运行中" : "未运行"}`;
    state.usingDefaultCredentials = Boolean(data.webui && data.webui.using_default_credentials);
    renderCredentialWarning();
  } catch (error) {
    document.getElementById("runtime-watch").textContent = `状态：${error.message}`;
  }
}

function renderCredentialWarning() {
  const banner = document.getElementById("credential-warning");
  if (!banner) return;
  banner.hidden = !state.usingDefaultCredentials;
}

async function openCredentialSettings() {
  selectView("config");
  await loadConfig();
  requestAnimationFrame(() => {
    const input = document.querySelector('#config-form [data-path="webui.username"]');
    if (input) {
      input.focus();
      input.scrollIntoView({ block: "center" });
    }
  });
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

async function loadDownloads(force = false) {
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

function stopInternalDownloadPolling() {
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

function internalDownloadID(item) {
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

function internalStatusLabel(status) {
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

function internalStatusClass(status) {
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

async function loadKV() {
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

async function startPhoneLogin() {
  state.loginMethod = "phone";
  updateLoginMethodUI();
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
  const input = document.getElementById("login-code");
  const code = input.value.trim();
  if (!code) {
    renderLoginError("请输入 Telegram 收到的原始验证码。");
    return;
  }
  const data = await loginRequest("/api/login/code", { code });
  if (data) input.value = "";
}

async function submitLoginPassword() {
  const input = document.getElementById("login-password");
  const password = input.value;
  if (!password) {
    renderLoginError("请输入 Telegram 2FA 密码。");
    return;
  }
  const data = await loginRequest("/api/login/password", { password });
  if (data) input.value = "";
}

function startSelectedLogin() {
  startPhoneLogin();
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
    return data;
  } catch (error) {
    renderLoginError(error.message);
    return null;
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
  data = data || {};
  state.lastLoginData = data;
  state.loginMethod = "phone";

  updateLoginMethodUI(data);
  renderLoginMeta(data);
  renderLoginSteps(data);
  renderLoginPanel(data);
  renderLoginResult(data);
  updateLoginActions(data);
  setLoginStatus(loginStatusMessage(data), loginStatusKind(data));
}

function updateLoginMethodUI(data = {}) {
  const phoneStart = document.getElementById("login-phone-start");
  if (phoneStart) phoneStart.hidden = false;

  const title = document.getElementById("login-method-title");
  const copy = document.getElementById("login-method-copy");
  if (title) title.textContent = "验证码登录";
  if (copy) copy.textContent = "输入完整手机号后，继续填写 Telegram 收到的验证码。";
}

function renderLoginMeta(data) {
  const meta = document.getElementById("login-meta");
  if (!meta) return;
  const items = [];
  if (data.kind) items.push(["方式", "验证码登录"]);
  if (data.namespace) items.push(["用户", data.namespace]);
  if (data.phone) items.push(["手机号", data.phone]);
  if (data.user) items.push(["Telegram", loginUserLabel(data.user)]);
  meta.innerHTML = items.map(([label, value]) => `
    <span><strong>${escapeHTML(label)}</strong>${escapeHTML(String(value || "-"))}</span>
  `).join("");
}

function renderLoginSteps(data) {
  const order = ["method", "auth", "password", "finish"];
  const current = currentLoginStep(data);
  const currentIndex = order.indexOf(current);
  document.querySelectorAll("[data-login-step]").forEach((item) => {
    const step = item.dataset.loginStep;
    const index = order.indexOf(step);
    const isCurrent = step === current;
    item.classList.toggle("is-current", isCurrent);
    item.classList.toggle("is-complete", index >= 0 && index < currentIndex);
    item.classList.toggle("is-error", step === "finish" && data.stage === "failed");
    if (isCurrent) {
      item.setAttribute("aria-current", "step");
    } else {
      item.removeAttribute("aria-current");
    }
  });
}

function currentLoginStep(data = {}) {
  if (data.stage === "done" || data.stage === "failed") return "finish";
  if (data.stage === "password") return "password";
  if (data.active && data.kind === "phone") return "auth";
  return "method";
}

function renderLoginPanel(data) {
  let panel = "login-step-method";
  if (data.stage === "done" || data.stage === "failed") {
    panel = "login-step-result";
  } else if (data.stage === "password") {
    panel = "login-step-password";
  } else if (data.active && data.kind === "phone") {
    panel = "login-step-code";
  }
  if (showLoginPanel(panel)) {
    focusLoginPanel(panel);
  }
}

function showLoginPanel(panelID) {
  if (state.loginPanel === panelID) return false;
  state.loginPanel = panelID;
  document.querySelectorAll(".login-step-panel").forEach((panel) => {
    const active = panel.id === panelID;
    panel.hidden = !active;
    panel.classList.remove("animate-in");
    if (active) {
      window.requestAnimationFrame(() => panel.classList.add("animate-in"));
    }
  });
  return true;
}

function focusLoginPanel(panelID) {
  const focusTargets = {
    "login-step-method": "login-phone",
    "login-step-code": "login-code",
    "login-step-password": "login-password",
  };
  const target = document.getElementById(focusTargets[panelID]);
  if (!target) return;
  window.setTimeout(() => target.focus(), 120);
}

function renderLoginResult(data) {
  if (data.stage !== "done" && data.stage !== "failed") return;
  const failed = data.stage === "failed";
  const mark = document.getElementById("login-result-mark");
  const title = document.getElementById("login-result-title");
  const copy = document.getElementById("login-result-copy");
  const finish = document.getElementById("finish-login-flow");
  if (mark) {
    mark.textContent = failed ? "!" : "✓";
    mark.classList.toggle("error", failed);
  }
  if (title) title.textContent = failed ? "登录失败" : "登录成功";
  if (copy) {
    copy.textContent = failed
      ? (data.error || data.status || "请检查输入后重新开始。")
      : `${loginUserLabel(data.user)} 已登录。`;
  }
  if (finish) finish.textContent = failed ? "留在此处" : "查看当前用户";
}

function updateLoginActions(data = {}) {
  const cancel = document.getElementById("cancel-login");
  if (cancel) cancel.hidden = !data.active;
}

function loginStatusMessage(data = {}) {
  if (data.stage === "failed") return data.error ? `登录失败：${data.error}` : "登录失败。";
  if (data.active && data.error) return data.error;
  if (data.stage === "done") return "登录成功。";
  if (data.stage === "password") return data.status || "请输入 2FA 密码。";
  if (data.active && data.kind === "phone") return data.status || "请输入 Telegram 收到的验证码。";
  if (data.status && data.status !== "当前没有登录流程。") return data.status;
  return "";
}

function loginStatusKind(data = {}) {
  if (data.error || data.stage === "failed") return "error";
  if (data.stage === "done") return "success";
  return "";
}

function loginUserLabel(user) {
  if (!user) return "-";
  return user.name || user.username || user.id || "-";
}

function resetLoginFlow() {
  state.lastLoginData = {};
  showLoginPanel("login-step-method");
  renderLoginSteps({});
  renderLoginMeta({});
  updateLoginActions({});
  setLoginStatus("");
}

function finishLoginFlow() {
  const done = state.lastLoginData && state.lastLoginData.stage === "done";
  if (!done) {
    resetLoginFlow();
    return;
  }
  const currentTab = document.querySelector('[data-user-tab="current"]');
  if (currentTab) currentTab.click();
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
  const runtimeLabel = update.docker
    ? "Docker 镜像"
    : (update.runtime === "binary" ? "本机二进制" : (update.runtime || "本机二进制"));
  const rows = [
    ["当前版本", update.current_version || "-"],
    ["当前提交", update.current_commit || "-"],
    ["构建日期", update.current_date || "-"],
    ["运行平台", `${update.goos || "-"} / ${update.goarch || "-"}`],
    ["运行方式", runtimeLabel],
    ["最新版本", update.latest_version || "-"],
    ["发布名称", update.latest_name || "-"],
    ["更新文件", update.asset_name || "-"],
    ["发布地址", update.latest_url || "-"],
  ];
  if (update.update_command) {
    rows.push(["更新命令", update.update_command]);
  }
  target.innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
  notes.textContent = update.release_notes || "";
  const kind = update.needs_update ? (update.can_update ? "success" : "warn") : "";
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
  initTagInputs(form);
  updateExclusiveListFields();
}

function renderField(field) {
  const [path, label, type, help, options] = field;
  const value = getPath(state.config, path);
  let control = "";
  if (type === "select") {
    control = `<select data-config-control data-path="${escapeAttr(path)}" data-type="${type}">
      ${(options || []).map((option) => `<option value="${escapeAttr(option)}" ${String(value) === option ? "selected" : ""}>${escapeHTML(option)}</option>`).join("")}
    </select>`;
  } else if (type === "proxy") {
    control = renderProxyInput(path, value || "");
  } else if (type === "bool") {
    control = `<label class="checkbox-line"><input data-config-control type="checkbox" data-path="${escapeAttr(path)}" data-type="${type}" ${value ? "checked" : ""}> 启用</label>`;
  } else if (type === "list" || type === "intList") {
    control = renderTagInput(path, type, value || []);
  } else if (type === "password") {
    control = `<input data-config-control type="password" data-path="${escapeAttr(path)}" data-type="${type}" value="" placeholder="留空保持不变">`;
  } else {
    control = `<input data-config-control type="${type === "number" ? "number" : "text"}" data-path="${escapeAttr(path)}" data-type="${type}" value="${escapeAttr(value ?? "")}">`;
  }
  return `
    <div class="field" data-field-path="${escapeAttr(path)}">
      <label>${escapeHTML(label)}</label>
      ${control}
      <small>${escapeHTML(help || path)}</small>
    </div>
  `;
}

function renderProxyInput(path, value) {
  const proxy = parseProxyValue(value);
  return `
    <div class="proxy-control" data-config-control data-path="${escapeAttr(path)}" data-type="proxy">
      <select data-proxy-scheme aria-label="代理协议">
        ${proxySchemes.map((scheme) => `<option value="${escapeAttr(scheme)}" ${proxy.scheme === scheme ? "selected" : ""}>${escapeHTML(scheme)}</option>`).join("")}
      </select>
      <input data-proxy-address type="text" value="${escapeAttr(proxy.address)}" placeholder="127.0.0.1:1080" autocomplete="off">
    </div>
  `;
}

function parseProxyValue(value) {
  value = String(value || "").trim();
  const fallback = { scheme: proxySchemes[0], address: "" };
  if (!value) return fallback;

  const matched = proxySchemes.find((scheme) => value.toLowerCase().startsWith(scheme));
  if (!matched) {
    return { scheme: fallback.scheme, address: trimProxyAddress(value) };
  }

  try {
    const parsed = new URL(value);
    return {
      scheme: `${parsed.protocol}//`,
      address: parsed.host || trimProxyAddress(value.slice(matched.length)),
    };
  } catch {
    return {
      scheme: matched,
      address: trimProxyAddress(value.slice(matched.length)),
    };
  }
}

function trimProxyAddress(value) {
  value = String(value || "").trim();
  const at = value.lastIndexOf("@");
  if (at >= 0) value = value.slice(at + 1);
  return value.replace(/^\/+/, "");
}

function renderTagInput(path, type, values) {
  const tags = (values || []).map((value) => renderTagItem(value)).join("");
  const placeholder = tagInputPlaceholder(path, type);
  return `
    <div class="tag-input" data-config-control data-path="${escapeAttr(path)}" data-type="${escapeAttr(type)}" aria-disabled="false">
      <div class="tag-list" data-tag-list>${tags}</div>
      <input class="tag-entry" data-tag-entry type="text" autocomplete="off" placeholder="${escapeAttr(placeholder)}">
    </div>
  `;
}

function tagInputPlaceholder(path, type) {
  if (path === "trigger_reactions") return "添加表情";
  if (type === "intList") return "添加 ID";
  return "添加词条";
}

function renderTagItem(value) {
  return `
    <span class="tag-item" data-tag-value="${escapeAttr(value)}">
      <span>${escapeHTML(value)}</span>
      <button class="tag-remove" data-tag-remove type="button" aria-label="移除 ${escapeAttr(value)}">x</button>
    </span>
  `;
}

function initTagInputs(root) {
  root.querySelectorAll(".tag-input").forEach((control) => {
    const input = control.querySelector("[data-tag-entry]");
    control.addEventListener("click", (event) => {
      const remove = event.target.closest("[data-tag-remove]");
      if (remove) {
        remove.closest("[data-tag-value]")?.remove();
        updateExclusiveListFields();
        return;
      }
      if (!input.disabled) input.focus();
    });
    input.addEventListener("keydown", (event) => {
      if (event.key === "Tab") {
        commitTagInput(control);
        updateExclusiveListFields();
        return;
      }
      if (event.key === "Enter" || event.key === ",") {
        if (input.value.trim()) {
          event.preventDefault();
          commitTagInput(control);
          updateExclusiveListFields();
        }
        return;
      }
      if (event.key === "Backspace" && !input.value) {
        const tags = control.querySelectorAll("[data-tag-value]");
        tags[tags.length - 1]?.remove();
        updateExclusiveListFields();
      }
    });
    input.addEventListener("input", updateExclusiveListFields);
    input.addEventListener("blur", () => {
      commitTagInput(control);
      updateExclusiveListFields();
    });
  });
}

function commitPendingTagInputs() {
  document.querySelectorAll("#config-form .tag-input").forEach(commitTagInput);
}

function commitTagInput(control) {
  const input = control.querySelector("[data-tag-entry]");
  if (!input || !input.value.trim()) return;
  splitList(input.value).forEach((value) => addTagValue(control, value));
  input.value = "";
}

function addTagValue(control, value) {
  value = String(value || "").trim();
  if (!value || tagValues(control).includes(value)) return;
  control.querySelector("[data-tag-list]").insertAdjacentHTML("beforeend", renderTagItem(value));
}

function tagValues(control) {
  return Array.from(control.querySelectorAll("[data-tag-value]")).map((tag) => tag.dataset.tagValue).filter(Boolean);
}

function listControlHasContent(control) {
  if (!control) return false;
  const pending = control.querySelector("[data-tag-entry]")?.value.trim();
  return tagValues(control).length > 0 || Boolean(pending);
}

function updateExclusiveListFields() {
  exclusiveListPairs.forEach(([leftPath, rightPath]) => {
    const left = document.querySelector(`.tag-input[data-path="${leftPath}"]`);
    const right = document.querySelector(`.tag-input[data-path="${rightPath}"]`);
    const leftHas = listControlHasContent(left);
    const rightHas = listControlHasContent(right);
    setListControlDisabled(right, leftHas && !rightHas);
    setListControlDisabled(left, rightHas && !leftHas);
  });
}

function setListControlDisabled(control, disabled) {
  if (!control) return;
  control.classList.toggle("disabled", disabled);
  control.setAttribute("aria-disabled", disabled ? "true" : "false");
  control.querySelectorAll("input, button").forEach((item) => {
    item.disabled = disabled;
  });
  const field = control.closest(".field");
  if (field) field.classList.toggle("disabled", disabled);
}

async function saveConfig(event) {
  event.preventDefault();
  commitPendingTagInputs();
  const status = document.getElementById("config-status");
  status.className = "notice";
  status.textContent = "正在保存...";
  const values = {};
  document.querySelectorAll("#config-form [data-config-control]").forEach((input) => {
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
    state.aria2Loaded = false;
    document.getElementById("aria2-frame").removeAttribute("src");
    if (document.getElementById("view-downloads").classList.contains("active")) {
      loadDownloads(true);
    }
    loadStatus();
    loadModules();
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function fieldValue(input, type) {
  if (input.classList.contains("tag-input")) {
    const values = tagValues(input);
    if (type === "intList") return values.map((value) => Number(value)).filter((value) => Number.isFinite(value));
    return values;
  }
  if (input.classList.contains("proxy-control")) return proxyFieldValue(input);
  if (type === "bool") return input.checked;
  if (type === "number") return Number(input.value || 0);
  if (type === "list") return splitList(input.value);
  if (type === "intList") return splitList(input.value).map((value) => Number(value)).filter((value) => Number.isFinite(value));
  return input.value;
}

function proxyFieldValue(control) {
  const scheme = control.querySelector("[data-proxy-scheme]")?.value || proxySchemes[0];
  const rawAddress = control.querySelector("[data-proxy-address]")?.value || "";
  const pasted = parseProxyValue(rawAddress);
  const address = rawAddress.includes("://") ? pasted.address : trimProxyAddress(rawAddress);
  if (!address) return "";
  return `${rawAddress.includes("://") ? pasted.scheme : scheme}${address}`;
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

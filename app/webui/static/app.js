const state = {
  config: null,
  aria2Loaded: false,
};

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
  loadStatus();
  loadAria2();
  loadKV();
  loadUser();
  loadConfig();
});

function bindNavigation() {
  document.querySelectorAll(".nav-item").forEach((button) => {
    button.addEventListener("click", () => {
      const view = button.dataset.view;
      document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item === button));
      document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
      if (view === "downloads") loadAria2();
      if (view === "kv") loadKV();
      if (view === "user") loadUser();
      if (view === "config") loadConfig();
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
  const data = text ? JSON.parse(text) : {};
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
  const status = document.getElementById("kv-status");
  status.className = "notice";
  status.textContent = "";
  body.innerHTML = `<tr><td colspan="7" class="empty">正在加载...</td></tr>`;
  try {
    const data = await api("/api/kv/links");
    if (data.status_error) {
      status.className = "notice error";
      status.textContent = `aria2 状态查询失败：${data.status_error}`;
    }
    const items = data.items || [];
    if (!items.length) {
      body.innerHTML = `<tr><td colspan="7" class="empty">没有下载链接记录</td></tr>`;
      return;
    }
    body.innerHTML = items.map(renderKVRow).join("");
    body.querySelectorAll("[data-delete-link]").forEach((button) => {
      button.addEventListener("click", async () => {
        const id = button.dataset.deleteLink;
        if (!confirm(`删除链接记录 ${id}？aria2 里的任务不会被删除。`)) return;
        await api(`/api/kv/links/${encodeURIComponent(id)}`, { method: "DELETE" });
        loadKV();
      });
    });
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
    body.innerHTML = `<tr><td colspan="7" class="empty">加载失败</td></tr>`;
  }
}

function renderKVRow(item) {
  const aria2 = (item.aria2 || []).map((entry) => {
    const progress = entry.total ? ` ${formatBytes(entry.completed)} / ${formatBytes(entry.total)}` : "";
    return `<div class="mono">${escapeHTML(entry.gid || "-")} <span class="pill">${escapeHTML(entry.status || "registered")}</span>${escapeHTML(progress)}</div>`;
  }).join("") || `<span class="pill warn">未提交</span>`;
  const expires = item.permanent ? "永久" : item.expires_at ? formatTime(item.expires_at) : "-";
  const expiredClass = item.expired ? "bad" : "warn";
  return `
    <tr>
      <td>
        <strong>${escapeHTML(item.file_name || item.id)}</strong>
        <div class="mono">${escapeHTML(item.id)}</div>
        <div>${formatBytes(item.file_size || 0)}</div>
      </td>
      <td class="mono"><a href="${escapeAttr(item.url)}" target="_blank" rel="noreferrer">${escapeHTML(item.url)}</a></td>
      <td>${aria2}</td>
      <td>${item.downloaded ? `<span class="pill">是</span>` : `<span class="pill ${expiredClass}">否</span>`}</td>
      <td>${formatTime(item.created_at)}</td>
      <td>${escapeHTML(expires)}</td>
      <td><button class="btn danger" data-delete-link="${escapeAttr(item.id)}">删除</button></td>
    </tr>
  `;
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

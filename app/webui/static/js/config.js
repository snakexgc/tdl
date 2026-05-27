// Config view: renders the config form from a field schema and saves changes.
import { state } from "./state.js";
import { api } from "./api.js";
import { escapeHTML, escapeAttr, getPath, splitList } from "./utils.js";
import { loadStatus } from "./status.js";
import { loadModules } from "./modules.js";
import { loadDownloads } from "./downloads.js";

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
      ["threads", "单文件线程数", "number", "与 tdl --threads 一致，限制单个文件最多同时使用多少个分片请求；小文件会自动降低实际线程数。"],
      ["limit", "并发下载数", "number", "与 tdl --limit 一致，限制同时下载的文件任务数量。"],
      ["pool_size", "DC 连接池大小", "number", "与 tdl --pool 一致，限制每个 Telegram DC 的连接池大小；填 0 表示无限。"],
      ["delay", "任务间隔", "number", "两个下载任务之间等待的秒数，通常为 0。"],
      ["ntp", "时间校准服务器", "text", "留空时启动会自动选择最快的内置服务器；手动填写后会优先检测该服务器。"],
      ["reconnect_timeout", "重连等待时间", "number", "网络断开后等待多久再重连，单位秒。"],
      ["download_dir", "下载目录规则", "text", "目录模板；可用 G 名称、P 来源 ID、I 触发消息文字、F 原始文件名、S/R 消息 ID、A 相册 ID、Y/M/D 日期，例如 G\\Y&M；I 会仅保留中英文数字并自动截断。"],
      ["filename", "文件名规则", "text", "文件名模板；与 download_dir 使用同一组变量，例如 G-I-F。"],
      ["filename_max_length", "文件名长度上限", "number", "最终文件名最长字符数；超长时优先缩短 I，默认 180。"],
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
      ["http.transfer_mode", "传输模式", "select", "source_parallel 为默认单 Range 模式；client_range 允许 aria2 多 Range。", ["source_parallel", "client_range"]],
      ["http.range_connections", "Range 连接数", "number", "仅 client_range 生效；填 0 表示 min(threads, 4)。"],
      ["http.buffer.mode", "下载缓冲", "select", "memory 为所有 HTTP 下载共享 chunk cache；off 表示只保留正在传输的分片。", ["memory", "off"]],
      ["http.buffer.size_mb", "缓冲大小", "number", "所有 HTTP 下载合计可使用的共享内存上限，单位 MiB；已读分片最多保留 5 秒。"],
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
      ["modules.http", "HTTP 下载代理", "bool", "启用后提供 /download 文件流链接；aria2 下载器依赖该模块。"],
      ["modules.forward", "监听转发", "bool", "启用后监听 forward.listen 中的 Telegram 对象并转发新消息。"],
    ],
  },
  {
    title: "下载器",
    fields: [
      ["downloader.mode", "下载器模式", "select", "aria2 使用外部 aria2；internal 使用 tdl 内部简易本地下载器；并发下载数由 limit 控制，单文件线程数由 threads 控制。", ["aria2", "internal"]],
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
      ["bot.notify.on_download_start", "下载开始通知", "bool", "开始下载时发送进度消息（带进度条）。"],
      ["bot.notify.on_download_complete", "下载完成通知", "bool", "下载完成后发送完成通知。"],
      ["bot.notify.on_download_pause", "下载暂停通知", "bool", "任务暂停时冻结进度消息并更新状态。"],
      ["bot.notify.on_download_error", "下载失败通知", "bool", "任务失败时冻结进度消息并显示错误。"],
      ["bot.notify.live_progress", "实时进度更新", "bool", "开启后开始下载时发送进度消息，每隔指定秒数自动编辑更新，完成/暂停/失败时冻结。"],
      ["bot.notify.live_progress_interval_seconds", "进度更新间隔（秒）", "number", "实时进度消息的编辑间隔，单位秒，最小 5 秒。"],
    ],
  },
  {
    title: "转发",
    fields: [
      ["forward.mode", "转发模式", "select", "default 优先官方转发，失败或受保护内容自动降级 clone；clone 始终复制发送。", ["default", "clone"]],
      ["forward.target", "默认目标", "text", "机器人 /forward 未指定目标、监听转发触发时使用；留空表示收藏夹。"],
      ["forward.listen", "监听对象", "list", "添加频道、群、用户的 ID 或用户名；频道会尝试同步监听关联评论区。"],
      ["forward.listen_comments", "监听频道评论", "bool", "开启后会读取频道关联讨论组 ID，并监听其中的评论消息；账号必须有权限访问该讨论组。"],
      ["forward.silent", "静默转发", "bool", "开启后转发消息不触发通知。"],
      ["forward.dedupe_ttl_seconds", "去重时间", "number", "监听转发的消息/相册去重时间，单位秒。"],
    ],
  },
];

export function initConfig() {
  document.getElementById("reload-config").addEventListener("click", loadConfig);
  document.getElementById("save-config").addEventListener("click", saveConfig);
  document.getElementById("reboot").addEventListener("click", reboot);
}

export async function loadConfig() {
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

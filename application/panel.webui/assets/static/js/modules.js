// Modules view: enable/disable runtime feature modules.
import { state } from "./state.js";
import { api } from "./api.js";
import { escapeHTML, escapeAttr } from "./utils.js";
import { loadStatus } from "./status.js";

export function initModules() {
  document.getElementById("refresh-modules").addEventListener("click", loadModules);
}

export async function loadModules() {
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
  renderModuleOverview();
  if (!state.modules.length) {
    target.innerHTML = `<div class="empty compact-empty">没有可管理的模块</div>`;
    return;
  }
  target.innerHTML = state.modules.map(renderModuleCard).join("");
  target.querySelectorAll("[data-module-toggle]").forEach((button) => {
    button.addEventListener("click", () => toggleModule(button.dataset.moduleToggle, button.dataset.nextEnabled === "true"));
  });
}

function renderModuleOverview() {
  const target = document.getElementById("module-overview");
  if (!target) return;
  if (!state.modules.length) {
    target.hidden = true;
    target.innerHTML = "";
    return;
  }

  const total = state.modules.length;
  const enabled = state.modules.filter((module) => module.enabled).length;
  const running = state.modules.filter((module) => module.running).length;
  const attention = state.modules.filter((module) => {
    const health = moduleHealth(module);
    return health.kind === "waiting" || health.kind === "error";
  }).length;
  const off = state.modules.filter((module) => !module.enabled).length;
  const cards = [
    ["enabled", "已启用", enabled, `共 ${total} 个模块`],
    ["running", "运行中", running, "正在提供服务"],
    ["attention", "需关注", attention, "已启用但未运行"],
    ["off", "已关闭", off, "当前停用"],
  ];

  target.innerHTML = cards.map(([kind, label, value, hint]) => `
    <div class="module-summary module-summary-${kind}">
      <span>${escapeHTML(label)}</span>
      <strong>${escapeHTML(value)}</strong>
      <small>${escapeHTML(hint)}</small>
    </div>
  `).join("");
  target.hidden = false;
}

function renderModuleCard(module) {
  const health = moduleHealth(module);
  const nextEnabled = !module.enabled;
  const toggleText = module.enabled ? "关闭" : "启用";
  const disabled = module.can_toggle ? "" : "disabled";
  const enabledLabel = module.enabled ? "已启用" : "已关闭";
  const runningLabel = module.running ? "运行中" : "未运行";
  const runningKind = moduleRunningStateKind(module, health);
  return `
    <section class="module-card module-card-${escapeAttr(health.kind)}">
      <div class="module-layout">
        <div class="module-state-stack" aria-label="模块状态">
          <div class="module-state-tile module-state-${module.enabled ? "enabled" : "disabled"}">
            <span>启用</span>
            <strong>${enabledLabel}</strong>
          </div>
          <div class="module-state-tile module-state-${runningKind}">
            <span>运行</span>
            <strong>${runningLabel}</strong>
          </div>
        </div>
        <div class="module-content">
          <div class="module-main">
            <div>
              <div class="module-title-line">
                <h2>${escapeHTML(module.name || module.id)}</h2>
                <span class="module-health-badge module-health-${escapeAttr(health.kind)}">${escapeHTML(health.label)}</span>
              </div>
              <p>${escapeHTML(module.description || "")}</p>
            </div>
          </div>
          <div class="module-foot">
            <div class="module-status">
              <span>当前状态</span>
              <strong>${escapeHTML(module.status || "-")}</strong>
            </div>
            <button class="btn ${module.enabled ? "danger" : "primary"}" data-module-toggle="${escapeAttr(module.id)}" data-next-enabled="${nextEnabled}" ${disabled}>${toggleText}</button>
          </div>
        </div>
      </div>
    </section>
  `;
}

function moduleRunningStateKind(module, health) {
  if (module.running) return "running";
  if (health.kind === "error") return "error";
  if (module.enabled) return "waiting";
  return "stopped";
}

function moduleHealth(module) {
  const status = String(module.status || "").trim();
  if (module.running) {
    return { kind: "running", label: "运行中", detail: "已启用并正在工作" };
  }
  if (!module.enabled) {
    return { kind: "off", label: "已关闭", detail: "模块当前停用" };
  }
  if (/失败|错误|异常|超时|已停止|error/i.test(status)) {
    return { kind: "error", label: "异常停止", detail: "已启用但没有运行" };
  }
  return { kind: "waiting", label: "待启动", detail: "已启用但没有运行" };
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

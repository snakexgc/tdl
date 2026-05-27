// Application entry point: loads view fragments, wires every page module, then
// hands control to the path router.
import { api } from "./api.js";
import { escapeHTML } from "./utils.js";
import { views, initRouter } from "./router.js";
import { loadStatus, openCredentialSettings } from "./status.js";
import { startHeartbeat, stopHeartbeat } from "./heartbeat.js";
import { initDashboard, stopDashboardPolling } from "./dashboard.js";
import { initDownloads, stopInternalDownloadPolling } from "./downloads.js";
import { initKV } from "./kv.js";
import { initUser } from "./user.js";
import { initConfig } from "./config.js";
import { initModules } from "./modules.js";
import { initUpdate } from "./update.js";

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
  initDashboard();
  initDownloads();
  initKV();
  initUser();
  initConfig();
  initModules();
  initUpdate();
  bindGlobalActions();
  loadStatus();
  initRouter();
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

function bindGlobalActions() {
  document.getElementById("logout").addEventListener("click", logout);
  const credentialWarningAction = document.getElementById("credential-warning-action");
  if (credentialWarningAction) {
    credentialWarningAction.addEventListener("click", openCredentialSettings);
  }
}

async function logout() {
  stopHeartbeat();
  stopInternalDownloadPolling();
  stopDashboardPolling();
  try {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
  } finally {
    window.location.href = "/login";
  }
}

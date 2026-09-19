import { api } from "./api.js";
import { escapeHTML } from "./utils.js";
import { initRouter, stopPages } from "./router.js";
import { loadStatus, renderStatus, openCredentialSettings } from "./status.js";
import { startRealtime, stopRealtime, observe } from "./events.js";

document.addEventListener("DOMContentLoaded", () => {
  bootstrap().catch(error => {
    const host = document.getElementById("view-host");
    if (host) host.innerHTML = `<div class="notice error">管理面板载入失败：${escapeHTML(error.message)}</div>`;
  });
});

async function bootstrap() {
  document.getElementById("logout").addEventListener("click", logout);
  document.getElementById("credential-warning-action")?.addEventListener("click", openCredentialSettings);
  await initRouter();
  window.addEventListener("configuration-changed", loadStatus);
  loadStatus();
  observe("status", (data, error) => { if (data && !error) renderStatus(data); });
  startRealtime();
}

async function logout() {
  stopRealtime();
  stopPages();
  try {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
  } finally {
    window.location.href = "/login";
  }
}

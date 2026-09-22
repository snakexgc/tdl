import { api } from "./api.js";
import { escapeHTML } from "./utils.js";
import { initRouter, stopPages, canLeavePage } from "./router.js";
import { loadStatus, renderStatus, showLoginCredentialWarning } from "./status.js";
import { startRealtime, stopRealtime, observe } from "./events.js";
import { initShell } from "./shell.js";

document.addEventListener("DOMContentLoaded", () => {
  bootstrap().catch(error => {
    const host = document.getElementById("view-host");
    if (host) host.innerHTML = `<div class="notice error">管理面板载入失败：${escapeHTML(error.message)}</div>`;
  });
});

async function bootstrap() {
  initShell();
  document.getElementById("logout").addEventListener("click", logout);
  window.addEventListener("configuration-changed", loadStatus);
  observe("status", (data, error) => { if (data && !error) renderStatus(data); });
  startRealtime();
  await Promise.all([initRouter(), loadStatus()]);
  showLoginCredentialWarning();
}

async function logout() {
  if (!canLeavePage()) return;
  stopRealtime();
  stopPages();
  try {
    await api("/api/auth/logout", { method: "POST", body: "{}" });
  } finally {
    window.location.href = "/login";
  }
}

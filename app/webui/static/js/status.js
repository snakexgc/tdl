// Runtime status shown in the sidebar plus the default-credentials warning banner.
import { state } from "./state.js";
import { api } from "./api.js";
import { navigate } from "./router.js";
import { loadConfig } from "./config.js";

export async function loadStatus() {
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

export async function openCredentialSettings() {
  navigate("config");
  await loadConfig();
  requestAnimationFrame(() => {
    const input = document.querySelector('#config-form [data-path="webui.username"]');
    if (input) {
      input.focus();
      input.scrollIntoView({ block: "center" });
    }
  });
}

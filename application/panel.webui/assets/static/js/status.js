// Runtime status shown in the sidebar plus the default-credentials warning banner.
import { state } from "./state.js";
import { api } from "./api.js";
import { navigate } from "./router.js";

let configurationVersion;

export async function loadStatus() {
  try {
    const data = await api("/api/status");
    renderStatus(data);
  } catch (error) {
    document.getElementById("runtime-watch").textContent = `状态：${error.message}`;
  }
}

export function renderStatus(data) {
 if (configurationVersion !== undefined && configurationVersion !== data.configuration_version) window.dispatchEvent(new CustomEvent("components-changed"));
 configurationVersion = data.configuration_version;
 const previous = state.downloaderMode;
    state.downloaderMode = data.downloader && data.downloader.mode ? data.downloader.mode : "aria2";
    const version = data.version || {};
    document.getElementById("runtime-version").textContent = `版本：${version.version || "-"}`;
    document.getElementById("runtime-namespace").textContent = `数据空间：${data.namespace || "-"}`;
    document.getElementById("runtime-watch").textContent = `监听：${data.watch_running ? "运行中" : "未运行"}`;
    state.usingDefaultCredentials = Boolean(data.webui && data.webui.using_default_credentials);
    renderCredentialWarning();
 if (previous !== state.downloaderMode) window.dispatchEvent(new CustomEvent("downloader-changed", { detail: data.downloader }));
}

function renderCredentialWarning() {
  const banner = document.getElementById("credential-warning");
  if (!banner) return;
  banner.hidden = !state.usingDefaultCredentials;
}

export async function openCredentialSettings() {
  await navigate("config");
  document.getElementById("view-config")?.dispatchEvent(new CustomEvent("open-settings", { bubbles: true }));
  requestAnimationFrame(() => {
    const input = document.querySelector('#config-form [data-path="webui.username"]');
    if (input) {
      input.focus();
      input.scrollIntoView({ block: "center" });
    }
  });
}

// Sidebar runtime status and the one-time default-credentials warning after login.
import { state } from "./state.js";
import { api } from "./api.js";
import { loadCurrentAccount } from "./account-profile.js";
import { navigate } from "./router.js";

let configurationVersion;
let accountNamespace;

export async function loadStatus() {
  try {
    const data = await api("/api/status");
    renderStatus(data);
  } catch (error) {
    document.getElementById("runtime-version").title =
      `状态读取失败：${error.message}`;
  }
}

export function renderStatus(data) {
  if (accountNamespace !== undefined && data.namespace !== accountNamespace) {
    window.location.reload();
    return;
  }
  accountNamespace = data.namespace;
  if (
    configurationVersion !== undefined &&
    configurationVersion !== data.configuration_version
  )
    window.dispatchEvent(new CustomEvent("components-changed"));
  configurationVersion = data.configuration_version;
  const previous = state.downloaderMode;
  state.downloaderMode =
    data.downloader && data.downloader.mode ? data.downloader.mode : "aria2";
  const version = data.version || {};
  document.getElementById("runtime-version").textContent =
    `版本：${version.version || "-"}`;
  void loadCurrentAccount(data.namespace);
  if (previous !== state.downloaderMode)
    window.dispatchEvent(
      new CustomEvent("downloader-changed", { detail: data.downloader }),
    );
}

export function showLoginCredentialWarning() {
  const warning = document.getElementById("credential-warning");
  if (!warning) return;
  try {
    const pending = window.sessionStorage.getItem("tdl-default-credentials-warning");
    window.sessionStorage.removeItem("tdl-default-credentials-warning");
    if (pending !== "1") return;
  } catch {
    return;
  }
  const dismiss = () => {
    clearTimeout(timer);
    if (warning.contains(document.activeElement))
      document.getElementById("view-host")?.focus({ preventScroll: true });
    warning.hidden = true;
  };
  document.getElementById("credential-warning-close").addEventListener("click", dismiss);
  document.getElementById("credential-warning-action").addEventListener("click", () => {
    dismiss();
    void openCredentialSettings();
  });
  warning.hidden = false;
  const timer = setTimeout(dismiss, 8000);
}

async function openCredentialSettings() {
  await navigate("/config?tab=system#setting-panel.webui-username");
  requestAnimationFrame(() => {
    const input = document.getElementById("setting-panel.webui-username");
    if (input) {
      input.focus();
      input.scrollIntoView({ block: "center" });
    }
  });
}

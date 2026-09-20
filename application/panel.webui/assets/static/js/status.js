// Runtime status shown in the sidebar plus the default-credentials warning banner.
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
  state.usingDefaultCredentials = Boolean(
    data.webui && data.webui.using_default_credentials,
  );
  renderCredentialWarning();
  if (previous !== state.downloaderMode)
    window.dispatchEvent(
      new CustomEvent("downloader-changed", { detail: data.downloader }),
    );
}

function renderCredentialWarning() {
  const banner = document.getElementById("credential-warning");
  if (!banner) return;
  banner.hidden = !state.usingDefaultCredentials;
}

export async function openCredentialSettings() {
  await navigate("/config?tab=panel#setting-panel.webui-username");
  requestAnimationFrame(() => {
    const input = document.getElementById("setting-panel.webui-username");
    if (input) {
      input.focus();
      input.scrollIntoView({ block: "center" });
    }
  });
}

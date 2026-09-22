// Browse releases first; downloading always uses the explicitly selected tag.
import { state } from "./state.js";
import { api } from "./api.js";
import { infoItem } from "./utils.js";

let selectedVersion = "";
let checkController = null;
let checking = false;
let applying = false;
let restarting = false;
let releaseButtons = [];

const element = id => document.getElementById(id);
const releases = () => [...(state.update?.stable_releases || []), ...(state.update?.preview_releases || [])];
const selectedRelease = () => releases().find(release => release.version === selectedVersion);

export function initUpdate() {
  element("check-update").addEventListener("click", loadUpdateStatus);
  element("apply-update").addEventListener("click", applyUpdate);
}

function notice(message, kind = "") {
  element("update-status").className = `notice ${kind}`.trim();
  element("update-status").textContent = message;
}

export async function loadUpdateStatus() {
  if (applying || restarting) return;
  checkController?.abort();
  const controller = new AbortController();
  checkController = controller;
  checking = true;
  state.update = null;
  renderUpdateInfo();
  notice("正在获取正式发行版本和预览版...");
  try {
    const data = await api("/api/update/check", { signal: controller.signal });
    if (controller !== checkController) return;
    state.update = data.update;
    if (!selectedRelease()) selectedVersion = releases()[0]?.version || "";
    checking = false;
    renderUpdateInfo();
    notice(data.update.message || "选择版本查看 Release Notes，确认后可下载并切换。", data.update.docker ? "warn" : "");
  } catch (error) {
    if (controller !== checkController || controller.signal.aborted) return;
    checking = false;
    renderUpdateInfo();
    notice(error.message, "error");
  } finally {
    if (controller === checkController) {
      checkController = null;
      checking = false;
      setControls();
    }
  }
}

function renderUpdateInfo() {
  const update = state.update;
  const rows = update ? [
    ["当前版本", update.current_version || "-"],
    ["当前提交", update.current_commit || "-"],
    ["构建日期", update.current_date || "-"],
    ["运行平台", `${update.goos || "-"} / ${update.goarch || "-"}`],
    ["运行方式", update.docker ? "Docker 容器" : "本机二进制"],
  ] : [];
  element("update-info").innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
  releaseButtons = [];
  renderChannel("update-stable", update?.stable_releases || []);
  renderChannel("update-preview", update?.preview_releases || []);
  renderSelection();
}

function formatDate(value) {
  const date = new Date(value);
  return value && Number.isFinite(date.getTime()) ? date.toLocaleString("zh-CN") : "发布日期未知";
}

function renderChannel(id, versions) {
  const target = element(id);
  target.replaceChildren();
  if (!versions.length) {
    const empty = document.createElement("p");
    empty.className = "update-empty";
    empty.textContent = checking ? "正在加载..." : (state.update ? "暂无已发布的版本" : "请检查更新以获取版本列表");
    target.append(empty);
    return;
  }
  versions.slice(0, 5).forEach(release => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "update-release";
    button.setAttribute("aria-controls", "update-notes-shell");
    const title = document.createElement("span");
    title.className = "update-release-title";
    const version = document.createElement("strong");
    version.textContent = release.version;
    const badge = document.createElement("span");
    badge.className = "update-release-badge";
    badge.textContent = release.current ? "当前版本" : (release.can_install ? "可切换" : "仅查看");
    title.append(version, badge);
    const date = document.createElement("span");
    date.className = "update-release-date";
    date.textContent = formatDate(release.published_at);
    button.append(title, date);
    button.addEventListener("click", () => {
      if (checking || applying || restarting) return;
      selectedVersion = release.version;
      renderSelection();
      element("update-notes-shell").scrollIntoView({ behavior: "smooth", block: "nearest" });
    });
    target.append(button);
    releaseButtons.push({ button, release });
  });
}

function renderSelection() {
  const release = selectedRelease();
  element("update-notes-title").textContent = release ? `Release Notes · ${release.version}` : "Release Notes";
  const notes = element("update-notes");
  if (release?.release_notes_html) {
    // Only insert HTML rendered by the server's safe Markdown renderer.
    notes.innerHTML = release.release_notes_html;
    notes.querySelectorAll("a").forEach(link => {
      link.target = "_blank";
      link.rel = "noopener noreferrer";
    });
  } else {
    notes.textContent = release ? (release.release_notes || "此版本未提供 Release Notes。") : "点击上方版本查看发布说明。";
  }
  notes.scrollTop = 0;
  element("update-selection").textContent = release ? `${release.prerelease ? "预览版" : "正式版"} · ${release.name || release.version} · ${formatDate(release.published_at)}` : "";
  element("update-selection-status").textContent = release ? `${release.message || ""}${release.asset_name ? ` · ${release.asset_name}` : ""}` : "";
  const link = element("update-release-link");
  link.hidden = true;
  link.removeAttribute("href");
  if (release?.url) {
    try {
      const url = new URL(release.url);
      if (["https:", "http:"].includes(url.protocol)) {
        link.href = url.href;
        link.hidden = false;
      }
    } catch { /* A missing or invalid release URL does not prevent viewing notes. */ }
  }
  setControls();
}

function setControls() {
  const release = selectedRelease();
  const busy = checking || applying || restarting;
  element("check-update").disabled = busy;
  const applyButton = element("apply-update");
  applyButton.disabled = busy || !release?.can_install || release.current || !!state.update?.docker;
  applyButton.textContent = applying ? "正在下载..." : restarting ? "正在重启..." : state.update?.docker ? "请更新容器镜像" : release?.current ? "当前版本" : "下载并切换";
  releaseButtons.forEach(({ button, release: choice }) => {
    button.disabled = busy;
    button.setAttribute("aria-pressed", String(choice.version === selectedVersion));
  });
}

async function applyUpdate() {
  const release = selectedRelease();
  if (checking || applying || restarting || !release?.can_install || release.current || state.update?.docker) return;
  const channel = release.prerelease ? "预览版" : "正式版";
  if (!confirm(`确认从 ${state.update.current_version || "当前版本"} 切换到${channel} ${release.version}？程序会下载所选版本并自动重启。`)) return;
  applying = true;
  setControls();
  notice(`正在下载 ${release.version}...`);
  try {
    const data = await api("/api/update/apply", { method: "POST", body: JSON.stringify({ version: release.version }) });
    restarting = true;
    notice(data.message || `更新包已下载，正在切换到 ${release.version} 并重启。`, "success");
  } catch (error) {
    notice(error.message, "error");
  } finally {
    applying = false;
    setControls();
  }
}

function stopUpdate() {
  checkController?.abort();
  checkController = null;
  checking = false;
}

export const page = { init: initUpdate, load: loadUpdateStatus, stop: stopUpdate };

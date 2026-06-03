// Update view: shows current/latest version and triggers self-update.
import { state } from "./state.js";
import { api } from "./api.js";
import { infoItem } from "./utils.js";

export function initUpdate() {
  document.getElementById("check-update").addEventListener("click", loadUpdateStatus);
  document.getElementById("apply-update").addEventListener("click", applyUpdate);
}

export async function loadUpdateStatus() {
  const status = document.getElementById("update-status");
  const target = document.getElementById("update-info");
  const notes = document.getElementById("update-notes");
  status.className = "notice";
  status.textContent = "正在检查更新...";
  target.innerHTML = "";
  notes.textContent = "";
  try {
    const data = await api("/api/update/check");
    state.update = data.update;
    renderUpdateInfo(data.update);
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

function renderUpdateInfo(update) {
  const status = document.getElementById("update-status");
  const target = document.getElementById("update-info");
  const notes = document.getElementById("update-notes");
  if (!update) {
    status.className = "notice";
    status.textContent = "";
    target.innerHTML = "";
    notes.textContent = "";
    return;
  }
  const runtimeLabel = update.docker
    ? "Docker 镜像"
    : (update.runtime === "binary" ? "本机二进制" : (update.runtime || "本机二进制"));
  const rows = [
    ["当前版本", update.current_version || "-"],
    ["当前提交", update.current_commit || "-"],
    ["构建日期", update.current_date || "-"],
    ["运行平台", `${update.goos || "-"} / ${update.goarch || "-"}`],
    ["运行方式", runtimeLabel],
    ["最新版本", update.latest_version || "-"],
    ["发布名称", update.latest_name || "-"],
    ["更新文件", update.asset_name || "-"],
    ["发布地址", update.latest_url || "-"],
  ];
  if (update.update_command) {
    rows.push(["更新方式", "使用 Docker Compose 更新容器"]);
    rows.push(["更新命令", update.update_command]);
  }
  target.innerHTML = rows.map(([label, value]) => infoItem(label, value)).join("");
  notes.textContent = update.release_notes || "";
  const kind = update.needs_update ? (update.can_update ? "success" : "warn") : "";
  status.className = `notice ${kind}`.trim();
  status.textContent = update.message || (update.needs_update ? "发现新版本。" : "当前已是最新版本。");
  const applyBtn = document.getElementById("apply-update");
  applyBtn.disabled = !update.needs_update || !update.can_update;
  applyBtn.textContent = update.docker ? "请使用 Docker Compose 更新" : "下载并更新";
}

async function applyUpdate() {
  if (!state.update) {
    await loadUpdateStatus();
  }
  if (!state.update || !state.update.needs_update || !state.update.can_update) {
    return;
  }
  if (!confirm(`确认更新到 ${state.update.latest_version}？程序会自动重启。`)) return;
  const status = document.getElementById("update-status");
  status.className = "notice";
  status.textContent = "正在下载更新...";
  try {
    const data = await api("/api/update/apply", { method: "POST", body: "{}" });
    if (data.update) {
      state.update = data.update;
      renderUpdateInfo(data.update);
    }
    status.className = "notice success";
    status.textContent = data.message || "更新包已下载，正在重启。";
  } catch (error) {
    status.className = "notice error";
    status.textContent = error.message;
  }
}

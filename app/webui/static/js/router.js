// Client-side path router built on the History API.
//
// Each view maps to a real path (/dashboard, /user, ...). The server serves the
// app shell for every such path, so a refresh or deep link lands on the right
// view instead of always falling back to the dashboard.
import { loadDashboard, stopDashboardPolling } from "./dashboard.js";
import { loadDownloads, stopInternalDownloadPolling } from "./downloads.js";
import { loadForwards, stopForwardPolling } from "./forwards.js";
import { loadKV } from "./kv.js";
import { loadUser, loadLoginStatus } from "./user.js";
import { loadModules } from "./modules.js";
import { loadConfig } from "./config.js";
import { loadUpdateStatus } from "./update.js";

export const views = ["dashboard", "user", "config", "downloads", "forwards", "kv", "modules", "update"];

const titles = {
  dashboard: "仪表盘",
  user: "用户管理",
  config: "配置文件",
  downloads: "下载管理",
  forwards: "转发监控",
  kv: "KV 管理",
  modules: "模块管理",
  update: "检查更新",
};

function viewFromPath(pathname) {
  const slug = (pathname || "/").replace(/^\/+/, "").replace(/\/.*$/, "");
  return views.includes(slug) ? slug : "dashboard";
}

function pathForView(view) {
  return `/${view}`;
}

export function initRouter() {
  bindNavigation();
  window.addEventListener("popstate", () => {
    const view = viewFromPath(window.location.pathname);
    applyView(view);
    loadViewData(view);
  });
  const view = viewFromPath(window.location.pathname);
  applyView(view);
  loadViewData(view);
}

function bindNavigation() {
  document.querySelectorAll(".nav-item").forEach((button) => {
    button.addEventListener("click", () => navigate(button.dataset.view));
  });
}

export function navigate(view) {
  if (!views.includes(view)) view = "dashboard";
  const path = pathForView(view);
  if (window.location.pathname !== path) {
    window.history.pushState({ view }, "", path);
  }
  applyView(view);
  loadViewData(view);
}

function applyView(view) {
  document.querySelectorAll(".nav-item").forEach((item) => item.classList.toggle("active", item.dataset.view === view));
  document.querySelectorAll(".view").forEach((item) => item.classList.toggle("active", item.id === `view-${view}`));
  document.title = `${titles[view] || "管理面板"} · TDL 管理面板`;
  if (view !== "dashboard") stopDashboardPolling();
  if (view !== "downloads") stopInternalDownloadPolling();
  if (view !== "forwards") stopForwardPolling();
}

function loadViewData(view) {
  if (view === "dashboard") loadDashboard();
  if (view === "downloads") loadDownloads();
  if (view === "forwards") loadForwards();
  if (view === "kv") loadKV();
  if (view === "user") {
    loadUser();
    loadLoginStatus();
  }
  if (view === "modules") loadModules();
  if (view === "config") loadConfig();
  if (view === "update") loadUpdateStatus();
}

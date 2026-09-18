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
import { api } from "./api.js";

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
  loadComponentNavigation();
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
  document.querySelectorAll(".nav-item[data-view]").forEach((button) => {
    button.addEventListener("click", () => navigate(button.dataset.view));
  });
}

async function loadComponentNavigation() {
  try {
    const data = await api("/api/components");
    const nav = document.querySelector("nav.nav");
    if (!nav) return;
    const seen = new Set();
    for (const component of data.components || []) {
      for (const page of component.pages || []) {
        const url = new URL(page.path, window.location.origin);
        if (url.origin !== window.location.origin || seen.has(url.pathname)) continue;
        seen.add(url.pathname);
        const view = url.pathname.replace(/^\//, "");
        const existing = Array.from(nav.querySelectorAll("[data-view]")).find(item => item.dataset.view === view);
        if (existing) {
          const label = existing.querySelector("span");
          if (label) label.textContent = page.title;
          existing.dataset.component = component.id;
          titles[view] = page.title;
        } else {
          const link = document.createElement("a");
          link.className = "nav-item";
          link.href = url.pathname + url.search + url.hash;
          link.textContent = page.title;
          link.dataset.component = component.id;
          nav.append(link);
        }
      }
    }
    applyView(viewFromPath(window.location.pathname));
  } catch {
    // Standalone and temporarily unavailable hosts retain their existing navigation.
  }
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

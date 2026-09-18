// Components declare their pages and lifecycle hooks; the shell has no feature list.
import { api } from "./api.js";
import { escapeHTML } from "./utils.js";

const pages = new Map();
let current;
let generation = 0;

function localPath(value) {
  const url = new URL(value, window.location.origin);
  if (url.origin !== window.location.origin || !value.startsWith("/") || value.startsWith("//")) {
    throw new Error("组件资源必须使用同源路径。");
  }
  return url.pathname;
}

export async function initRouter() {
  const data = await api("/api/components");
  const declarations = (data.components || []).filter(component => component.enabled !== false)
    .flatMap(component => (component.pages || []).map(page => ({ ...page, owner: component.id })))
    .sort((a, b) => (a.order || 100) - (b.order || 100) || a.path.localeCompare(b.path));
  const host = document.getElementById("view-host");
  const nav = document.querySelector("nav.nav");
  host.replaceChildren();
  nav.replaceChildren();
  for (const page of declarations) {
    const path = localPath(page.path);
    const link = document.createElement("a");
    link.className = "nav-item";
    link.href = path;
    link.textContent = page.title;
    link.dataset.component = page.owner;
    if (page.view) {
      if (!/^[a-z][a-z0-9_-]*$/.test(page.view) || pages.has(path)) throw new Error("无效的组件页面声明。");
      const section = document.createElement("div");
      section.className = "view";
      section.id = `page-${page.view}`;
      host.append(section);
      if (page.style) {
        const style = document.createElement("link");
        style.rel = "stylesheet";
        style.href = localPath(page.style);
        document.head.append(style);
      }
      if (page.module) page.module = localPath(page.module);
      pages.set(path, { ...page, path, section, link });
      link.dataset.view = page.view;
      link.addEventListener("click", event => {
        if (event.button || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        event.preventDefault();
        navigate(path);
      });
    }
    nav.append(link);
  }
  window.addEventListener("popstate", () => { void show(window.location.pathname); });
  window.addEventListener("pagehide", stopPages);
  await show(window.location.pathname);
}

export function stopPages() {
  ++generation;
  for (const page of pages.values()) page.hooks?.stop?.();
}

export function navigate(view) {
  const path = view.startsWith("/") ? view : `/${view}`;
  if (window.location.pathname !== path) window.history.pushState({}, "", path);
  return show(path);
}

async function show(path) {
  const page = pages.get(path) || (path === "/" ? pages.values().next().value : null);
  if (current !== page) current?.hooks?.stop?.();
  current = page;
  const activeGeneration = ++generation;
  for (const entry of pages.values()) {
    entry.section.classList.toggle("active", entry === page);
    entry.section.querySelectorAll(".view").forEach(view => view.classList.toggle("active", entry === page));
    entry.link.classList.toggle("active", entry === page);
  }
  let unavailable = document.getElementById("page-unavailable");
  if (!page) {
    if (!unavailable) {
      unavailable = document.createElement("div");
      unavailable.id = "page-unavailable";
      unavailable.className = "notice error";
      document.getElementById("view-host").append(unavailable);
    }
    unavailable.hidden = false;
    unavailable.textContent = "页面不可用，所属组件可能已停用。";
    return;
  }
  if (unavailable) unavailable.hidden = true;
  document.title = `${page.title} · TDL 管理面板`;
  try {
    page.ready ||= mount(page);
    await page.ready;
    if (activeGeneration === generation) await page.hooks?.load?.();
    if (current !== page) page.hooks?.stop?.();
  } catch (error) {
    page.section.innerHTML = `<div class="notice error">${escapeHTML(error.message)}</div>`;
    page.ready = null;
  }
}

async function mount(page) {
  const response = await fetch(`/views/${page.view}.html`, { credentials: "same-origin" });
  if (response.status === 401) { window.location.href = "/login"; throw new Error("登录已过期。"); }
  if (!response.ok) throw new Error(`无法载入 ${page.title}`);
  page.section.innerHTML = await response.text();
  // Legacy fragments include their own view root. Visibility belongs to the shell.
  page.section.querySelectorAll(".view").forEach(view => view.classList.toggle("active", current === page));
  page.hooks = page.module ? (await import(page.module)).page : {};
  await page.hooks?.init?.();
}

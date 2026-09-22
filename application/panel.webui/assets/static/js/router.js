// The catalog owns navigation. Query parameters belong to the active page.
import { api } from "./api.js";
import { mountSettingsTabs } from "./settings-tabs.js";
import { escapeHTML } from "./utils.js";

const pages = new Map();
let current,
  currentURL,
  generation = 0,
  navigationGeneration = 0;
function localURL(value) {
  const url = new URL(value, window.location.origin);
  if (
    url.origin !== window.location.origin ||
    !value.startsWith("/") ||
    value.startsWith("//")
  )
    throw new Error("组件资源必须使用同源路径。");
  return url;
}
const locationURL = () =>
  window.location.pathname +
  (window.location.search || "") +
  (window.location.hash || "");
const relative = (url) => url.pathname + url.search + url.hash;

export async function initRouter() {
  pages.clear();
  document.getElementById("view-host").replaceChildren();
  reconcileNavigation(await api("/api/components"));
  window.addEventListener("components-changed", () => {
    void refreshNavigation();
  });
  window.addEventListener("popstate", () => {
    const target = resolve(locationURL());
    if (!canLeavePage(target.pathname)) {
      window.history.pushState({}, "", currentURL);
      return;
    }
    void show(target);
  });
  window.addEventListener("pagehide", stopPages);
  window.addEventListener("pageshow", (event) => {
    if (event.persisted) void show(resolve(locationURL()));
  });
  window.addEventListener("beforeunload", (event) => {
    if (current?.hooks?.dirty?.()) {
      event.preventDefault();
      event.returnValue = "";
    }
  });
  document.addEventListener?.("click", (event) => {
    const link = event.target.closest?.("a[data-app-link]");
    if (
      !link ||
      event.button ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    void navigate(link.getAttribute("href"));
  });
  await show(resolve(locationURL()));
}
async function refreshNavigation() {
  const version = ++navigationGeneration;
  try {
    const data = await api("/api/components");
    if (version !== navigationGeneration) return;
    const wasEnabled = current?.ownerEnabled;
    reconcileNavigation(data);
    if (current && (!current.enabled || wasEnabled !== current.ownerEnabled))
      await show(resolve(locationURL()));
  } catch (error) {
    console.error(error);
  }
}
function reconcileNavigation(data) {
  const declarations = (data.components || [])
    .flatMap((component) =>
      (component.pages || [])
        .filter(
          (page) =>
            (component.active_enabled ?? component.enabled) !== false ||
            page.keep_visible ||
            page.nav_hidden,
        )
        .map((page) => ({
          ...page,
          owner: component.id,
          ownerEnabled:
            (component.active_enabled ?? component.enabled) !== false,
        })),
    )
    .sort(
      (a, b) =>
        (a.order || 100) - (b.order || 100) || a.path.localeCompare(b.path),
    );
  const host = document.getElementById("view-host"),
    nav = document.querySelector("nav.nav");
  nav.replaceChildren();
  for (const page of pages.values()) page.enabled = false;
  for (const declaration of declarations) {
    const path = localURL(declaration.path).pathname;
    let page = pages.get(path);
    if (!page) {
      const link = document.createElement("a");
      link.className = "nav-item";
      link.href = path;
      link.textContent = declaration.title;
      link.dataset.component = declaration.owner;
      page = { ...declaration, path, link };
      if (page.view) {
        if (!/^[a-z][a-z0-9_-]*$/.test(page.view))
          throw new Error("无效的组件页面声明。");
        const section = document.createElement("div");
        section.className = "view";
        section.id = `page-${page.view}`;
        host.append(section);
        page.section = section;
        if (page.module) page.module = localURL(page.module).pathname;
      }
      if (page.style) {
        const style = document.createElement("link");
        style.rel = "stylesheet";
        style.href = localURL(page.style).pathname;
        document.head.append(style);
      }
      if (page.view) {
        link.dataset.view = page.view || "";
        link.addEventListener("click", (event) => {
          if (
            event.button ||
            event.ctrlKey ||
            event.metaKey ||
            event.shiftKey ||
            event.altKey
          )
            return;
          event.preventDefault();
          void navigate(path);
        });
      }
      pages.set(path, page);
    }
    page.enabled = true;
    page.ownerEnabled = declaration.ownerEnabled;
    if (!page.nav_hidden) nav.append(page.link);
  }
}
function resolve(value) {
  let url = localURL(value);
  if (url.pathname === "/")
    url = localURL(
      [...pages.values()].find(
        (page) => page.enabled && !page.nav_hidden && page.view,
      )?.path || "/dashboard",
    );
  return url;
}
export function canLeavePage(nextPath) {
  return (
    nextPath === current?.path || current?.hooks?.beforeLeave?.() !== false
  );
}
export function stopPages() {
  ++generation;
  for (const page of pages.values()) page.hooks?.stop?.();
}
export async function navigate(value) {
  const url = resolve(value.startsWith("/") ? value : `/${value}`);
  if (!canLeavePage(url.pathname)) return false;
  if (relative(url) !== locationURL())
    window.history.pushState({}, "", relative(url));
  await show(url);
  return true;
}
async function show(url) {
  const candidate = pages.get(url.pathname),
    page = candidate?.enabled && candidate.section ? candidate : null;
  if (current !== page) {
    current?.hooks?.stop?.();
    if (window.dispatchEvent) window.dispatchEvent(new Event("page-changing"));
  }
  current = page;
  currentURL = relative(url);
  if (locationURL() !== currentURL)
    window.history.replaceState?.({}, "", currentURL);
  const version = ++generation;
  for (const entry of pages.values()) {
    entry.section?.classList.toggle("active", entry === page);
    entry.section
      ?.querySelectorAll(".view")
      .forEach((view) => view.classList.toggle("active", entry === page));
    entry.link.classList.toggle("active", entry === page);
    entry.link.ariaCurrent = entry === page ? "page" : null;
  }
  const breadcrumb = document.getElementById("current-page");
  if (breadcrumb) breadcrumb.textContent = page?.title || "页面不可用";
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
  if (!page.ownerEnabled) {
    page.hooks?.stop?.();
    if (page.ready) {
      page.hooks?.dispose?.();
      page.ready = null;
      page.hooks = null;
    }
    page.section.innerHTML = `<header class="page-head"><h1>${escapeHTML(page.title)}</h1></header><div class="empty">相关服务已停用。<p><a class="btn primary" data-app-link href="/modules">管理模块</a>${page.settings_url && !page.settings_url.startsWith("/modules") ? ` <a class="btn" data-app-link href="${escapeHTML(page.settings_url)}">相关设置</a>` : ""}</p></div>`;
    return;
  }
  try {
    page.ready ||= mount(page);
    await page.ready;
    if (version === generation) {
      if (page.tabs?.settingsActive()) await page.tabs.load();
      else await page.hooks?.load?.(url);
    }
    if (current !== page) page.hooks?.stop?.();
  } catch (error) {
    page.section.innerHTML = `<div class="notice error">${escapeHTML(error.message)}</div>`;
    page.ready = null;
  }
}
async function mount(page) {
  const response = await fetch(`/views/${page.view}.html`, {
    credentials: "same-origin",
  });
  if (response.status === 401) {
    window.location.href = "/login";
    throw new Error("登录已过期。");
  }
  if (!response.ok) throw new Error(`无法载入 ${page.title}`);
  page.section.innerHTML = await response.text();
  page.section
    .querySelectorAll(".view")
    .forEach((view) => view.classList.toggle("active", current === page));
  page.hooks = page.module ? (await import(page.module)).page : {};
  await page.hooks?.init?.();
  if (page.settings?.length)
    page.tabs = mountSettingsTabs(page, () => current === page);
}

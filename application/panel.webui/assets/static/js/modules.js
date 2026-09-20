import { api } from "./api.js";
import { element, button } from "./ui.js";
import { featureGroups, componentStatus } from "./module-model.js";
import { loadStatus } from "./status.js";

let components = [],
  canToggle = false,
  timer,
  request,
  saving = false,
  active = false;
const $ = (id) => document.getElementById(id);
const compactLayout = window.matchMedia("(max-width: 1200px)");
function relayout() {
  if (active) render();
}
function report(text, kind = "") {
  $("module-status").textContent = text;
  $("module-status").className = `notice ${kind}`;
}

export function initModules() {
  $("refresh-modules").addEventListener("click", () => void refresh());
}

async function refresh() {
  if (request || saving || !active) return;
  const controller = new AbortController();
  request = controller;
  $("refresh-modules").disabled = true;
  try {
    const options = {
      signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
    };
    const data = await api("/api/components", options);
    if (controller.signal.aborted) return;
    components = data.components || [];
    canToggle = Boolean(data.can_toggle && data.editable);
    render();
    if ($("module-status").dataset.stale) {
      report("");
      delete $("module-status").dataset.stale;
    }
  } catch (error) {
    if (!controller.signal.aborted) {
      report(`状态读取失败，已保留上次结果：${error.message}`, "error");
      $("module-status").dataset.stale = "true";
    }
  } finally {
    if (request === controller) {
      request = null;
      $("refresh-modules").disabled = saving;
    }
  }
}

function link(title, href) {
  const item = element("a", title, "btn secondary");
  item.href = href;
  item.dataset.appLink = "";
  return item;
}

function render() {
  const focus = document.activeElement?.getAttribute("data-module-focus");
  const expanded = new Set(
    [...$("module-list").querySelectorAll("details[open]")].map(
      (item) => item.id,
    ),
  );
  const first = !$("module-list").children.length;
  const display = components;
  const groups = featureGroups(display);
  const overview = $("module-overview");
  overview.replaceChildren();
  for (const [label, value] of [
    ["功能", groups.length],
    ["细分模块", display.length],
    [
      "运行中",
      display.filter((c) => componentStatus(c).kind === "running").length,
    ],
    [
      "需关注",
      display.filter((c) =>
        ["error", "waiting"].includes(componentStatus(c).kind),
      ).length,
    ],
  ]) {
    const item = element("div", null, "module-summary");
    item.append(element("span", label), element("strong", String(value)));
    overview.append(item);
  }
  overview.hidden = false;
  $("module-list").replaceChildren();
  // Each column stacks independently; changing one card's height only moves
  // the cards beneath it. A single column preserves manifest order on mobile.
  const columns = Array.from({ length: compactLayout.matches ? 1 : 2 }, () =>
    element("div", null, "module-column"),
  );
  if (groups.length) $("module-list").append(...columns);
  for (const [index, group] of groups.entries()) {
    const card = element("details", null, "feature-card");
    card.id = `feature-${group.id}`;
    card.open = first || expanded.has(card.id);
    const summary = element("summary", null, "feature-summary");
    summary.dataset.moduleFocus = `summary:${group.id}`;
    const title = element("div", null, "feature-title");
    const running = group.components.filter(
      (c) => componentStatus(c).kind === "running",
    ).length;
    const attention = group.components.filter((c) =>
      ["error", "waiting"].includes(componentStatus(c).kind),
    ).length;
    title.append(
      element("h2", group.title),
      element(
        "span",
        `${group.components.length} 个细分模块 · ${running} 个运行中`,
        "subtle",
      ),
    );
    summary.append(
      title,
      element(
        "span",
        attention ? `${attention} 项需关注` : running ? "运行正常" : "已停用",
        `feature-health ${attention ? "warn-text" : "subtle"}`,
      ),
    );
    card.append(summary);
    const body = element("div", null, "feature-components");
    for (const component of group.components) {
      const row = element("div", null, "feature-component");
      row.dataset.component = component.id;
      const name = element("div", null, "component-name");
      name.append(
        element("strong", component.title || component.id),
        element("small", component.id, "subtle"),
      );
      if (component.error)
        name.append(element("p", component.error, "bad-text"));
      const status = componentStatus(component);
      const badge = element(
        "span",
        status.text,
        `component-status component-status-${status.kind}`,
      );
      row.append(name, badge);
      const control = button(
        component.enabled === false ? "启用" : "停用",
        () => void toggle(component),
      );
      control.dataset.moduleFocus = `toggle:${component.id}`;
      control.setAttribute(
        "aria-label",
        `${component.enabled === false ? "启用" : "停用"} ${component.title || component.id}`,
      );
      control.disabled = saving || !canToggle;
      row.append(control);
      const logs = link(
        "日志",
        `/logs?component=${encodeURIComponent(component.id)}`,
      );
      logs.setAttribute(
        "aria-label",
        `查看 ${component.title || component.id} 的日志`,
      );
      row.append(logs);
      body.append(row);
    }
    if (group.settings_url) {
      const settings = link("相关设置", group.settings_url);
      settings.dataset.moduleFocus = `settings:${group.id}`;
      body.append(settings);
    }
    card.append(body);
    columns[index % columns.length].append(card);
  }
  if (!groups.length)
    $("module-list").append(element("p", "暂无已声明的功能模块。", "empty"));
  $("module-mode").textContent =
    "启停选择保存到配置文件，重启后生效。前往设置页可核对全部修改并重启。";
  if (focus)
    [...$("module-list").querySelectorAll("[data-module-focus]")]
      .find((item) => item.dataset.moduleFocus === focus)
      ?.focus({ preventScroll: true });
}

async function toggle(component) {
  if (saving) return;
  const enabled = component.enabled === false;
  const panelWarning = component.pages?.some(
    (page) => page.view === "dashboard",
  )
    ? "面板停用后需从配置文件重新启用。"
    : "";
  if (
    !enabled &&
    !confirm(
      `确认将「${component.title || component.id}」保存为停用？重启后其依赖服务和任务可能暂停。${panelWarning}`,
    )
  )
    return;
  saving = true;
  request?.abort();
  request = null;
  $("refresh-modules").disabled = true;
  render();
  report("正在保存启停设置…");
  try {
    const result = await api("/api/components", {
      method: "PATCH",
      body: JSON.stringify({
        id: component.id,
        enabled,
        revision: component.revision || "",
      }),
    });
    components = result.components || components;
    report("启停设置已保存。请前往设置页确认修改并重启后生效。", "success");
    window.dispatchEvent(new Event("components-changed"));
    void loadStatus();
  } catch (error) {
    report(
      error.status === 409 ? "配置已变化，请刷新状态后重试。" : error.message,
      "error",
    );
  } finally {
    saving = false;
    $("refresh-modules").disabled = false;
    render();
    await refresh();
  }
}

export async function loadModules(url) {
  active = true;
  compactLayout.addEventListener("change", relayout);
  if (components.length) render();
  clearInterval(timer);
  await refresh();
  if (!active) return;
  timer = setInterval(() => {
    if (!document.hidden) void refresh();
  }, 10000);
  const anchor =
    url?.hash && document.getElementById(decodeURIComponent(url.hash.slice(1)));
  if (anchor) {
    if (anchor.tagName === "DETAILS") anchor.open = true;
    anchor.scrollIntoView({ block: "start" });
  }
}
function stop() {
  active = false;
  compactLayout.removeEventListener("change", relayout);
  clearInterval(timer);
  request?.abort();
  request = null;
}
function beforeLeave() {
  if (saving) report("正在更新模块状态，请等待完成。");
  return !saving;
}
export const page = { init: initModules, load: loadModules, stop, beforeLeave };

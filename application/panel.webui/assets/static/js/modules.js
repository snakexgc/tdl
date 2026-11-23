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
    const nextComponents = data.components || [];
    const nextCanToggle = Boolean(data.can_toggle && data.editable);
    const changed = canToggle !== nextCanToggle || JSON.stringify(components) !== JSON.stringify(nextComponents);
    components = nextComponents;
    canToggle = nextCanToggle;
    if (changed || $("module-overview").hidden) render();
    if ($("module-status").dataset.stale) {
      report("");
      delete $("module-status").dataset.stale;
    }
  } catch (error) {
    if (!controller.signal.aborted) {
      const retained = $("module-overview").hidden ? "" : "，已保留上次结果";
      report(`状态读取失败${retained}：${error.message}`, "error");
      $("module-status").dataset.stale = "true";
      if ($("module-loading")) $("module-loading").hidden = true;
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
  const list = $("module-list");
  const focus = document.activeElement?.getAttribute("data-module-focus");
  const expanded = new Set(
    [...list.querySelectorAll("details[open]")].map((item) => item.id),
  );
  const overview = $("module-overview");
  const statusByID = new Map(components.map((component) => [component.id, componentStatus(component)]));
  const counts = { running: 0, waiting: 0, error: 0, off: 0 };
  for (const status of statusByID.values()) counts[status.kind]++;
  const groups = featureGroups(components);
  overview.replaceChildren();
  for (const [label, value, state] of [
    ["组件总数", components.length, "total"],
    ["运行中", counts.running, "running"],
    ["需关注", counts.error + counts.waiting, "attention"],
    ["已停用", counts.off, "off"],
  ]) {
    const item = element("div", null, "module-overview-item");
    item.dataset.metric = value ? state : "neutral";
    item.append(element("dt", label), element("dd", String(value)));
    overview.append(item);
  }
  overview.hidden = false;
  list.replaceChildren();
  for (const group of groups) {
    const card = element("details", null, "feature-card");
    card.id = `feature-${group.id}`;
    const summary = element("summary", null, "feature-summary");
    summary.dataset.moduleFocus = `summary:${group.id}`;
    const title = element("span", null, "feature-title");
    const kinds = group.components.map((component) => statusByID.get(component.id).kind);
    const running = kinds.filter((kind) => kind === "running").length;
    const attention = kinds.filter((kind) => kind === "error" || kind === "waiting").length;
    card.open = expanded.has(card.id);
    title.append(
      element("strong", group.title),
      element(
        "span",
        `${group.components.length} 个组件`,
        "subtle",
      ),
    );
    summary.append(
      title,
      element(
        "span",
        attention ? `${attention} 项需关注` : running ? `${running} 个运行中` : "已停用",
        `feature-health ${attention ? "feature-health-attention" : running ? "feature-health-running" : ""}`,
      ),
    );
    card.append(summary);
    const body = element("div", null, "feature-body");
    const rows = element("ul", null, "feature-components");
    for (const component of group.components) {
      const row = element("li", null, "feature-component");
      row.dataset.component = component.id;
      const name = element("div", null, "component-name");
      name.append(
        element("strong", component.title || component.id),
        element("small", component.id, "subtle"),
      );
      if (component.error)
        name.append(element("p", component.error, "bad-text"));
      const status = statusByID.get(component.id);
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
      control.disabled = !canToggle;
      if (saving && canToggle) control.setAttribute("aria-disabled", "true");
      if (component.id === "proxy.range") {
        control.disabled = true;
        control.textContent = "始终启用";
        control.setAttribute("aria-label", "HTTP 下载服务始终启用");
      }
      const logs = link(
        "日志",
        `/logs?component=${encodeURIComponent(component.id)}`,
      );
      logs.dataset.moduleFocus = `logs:${component.id}`;
      logs.setAttribute(
        "aria-label",
        `查看 ${component.title || component.id} 的日志`,
      );
      const actions = element("div", null, "feature-actions");
      actions.append(control, logs);
      row.append(actions);
      rows.append(row);
    }
    body.append(rows);
    if (group.settings_url) {
      const settings = link("相关设置", group.settings_url);
      settings.dataset.moduleFocus = `settings:${group.id}`;
      const footer = element("div", null, "feature-settings");
      footer.append(settings);
      body.append(footer);
    }
    card.append(body);
    list.append(card);
  }
  if (!groups.length)
    list.append(element("p", "暂无已声明的功能模块。", "empty"));
  if (focus)
    [...list.querySelectorAll("[data-module-focus]")]
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
  if (components.length) render();
  else if ($("module-loading")) $("module-loading").hidden = false;
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
  clearInterval(timer);
  request?.abort();
  request = null;
}
function beforeLeave() {
  if (saving) report("正在更新模块状态，请等待完成。");
  return !saving;
}
export const page = { init: initModules, load: loadModules, stop, beforeLeave };

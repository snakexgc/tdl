import { api } from "./api.js";
import { navigate } from "./router.js";
import {
  element,
  button,
  openDrawer,
  tabKeyboard,
  activateTabs,
} from "./ui.js";
import { featureGroups } from "./module-model.js";
import { renderDiagnostics } from "./module-diagnostics.js";

let active = false,
  live = true,
  timer,
  request,
  currentURL,
  data,
  health,
  failure = "",
  generation = 0;
const $ = (id) => document.getElementById(id);
const levels = {
  debug: "调试",
  info: "信息",
  warn: "警告",
  error: "错误",
  dpanic: "错误",
  panic: "错误",
  fatal: "致命",
};
const keys = ["feature", "component", "level", "kind", "range", "q"];
const controls = (key) => $(key === "q" ? "logs-query" : `logs-${key}`);
function endpoint(download = false) {
  const params = new URLSearchParams(currentURL.search);
  params.delete("tab");
  params.delete("range");
  const range = Number(currentURL.searchParams.get("range"));
  if ([15, 60, 1440].includes(range))
    params.set("since", new Date(Date.now() - range * 60000).toISOString());
  if (download) {
    params.delete("before");
    params.set("download", "1");
  }
  return `/api/logs?${params}`;
}
function options(select, entries, selected) {
  select.replaceChildren();
  for (const [value, label] of entries) {
    const option = element("option", label);
    option.value = value;
    select.append(option);
  }
  if (selected && !entries.some(([value]) => value === selected)) {
    const option = element("option", selected);
    option.value = selected;
    select.append(option);
  }
  select.value = selected || "";
}
function filters() {
  const components = data?.components || [],
    feature = currentURL.searchParams.get("feature") || "";
  options(
    $("logs-feature"),
    [
      ["", "全部功能模块"],
      ...featureGroups(components).map((group) => [group.id, group.title]),
    ],
    feature,
  );
  options(
    $("logs-component"),
    [
      ["", "全部细分模块"],
      ...components
        .filter((item) => !feature || item.feature?.id === feature)
        .map((item) => [item.id, item.title]),
    ],
    currentURL.searchParams.get("component"),
  );
}
function apply() {
  const url = new URL(currentURL);
  url.searchParams.delete("before");
  for (const key of keys) {
    const value = controls(key).value.trim();
    if (value) url.searchParams.set(key, value);
    else url.searchParams.delete(key);
  }
  void navigate(url.pathname + url.search);
}
function showDetail(item) {
  const content = element("div", null, "log-detail");
  content.append(
    element(
      "p",
      `${new Date(item.at).toLocaleString()} · ${levels[item.level] || item.level}`,
    ),
    element("p", item.component_title),
    element("pre", item.message),
  );
  if (item.details) {
    let details = item.details;
    try {
      details = JSON.stringify(JSON.parse(details), null, 2);
    } catch {}
    content.append(element("h3", "详细信息"), element("pre", details));
  }
  if (item.logger || item.caller)
    content.append(
      element("p", `${item.logger || ""} ${item.caller || ""}`, "subtle"),
    );
  openDrawer("日志详情", content);
}
function renderRows() {
  const rows = $("logs-rows");
  rows.replaceChildren();
  const items = data?.items || [];
  const groups = new Map(
    (data?.components || []).map((component) => [
      component.feature.id,
      component.feature.title,
    ]),
  );
  for (const item of items) {
    const row = element("tr");
    row.append(element("td", new Date(item.at).toLocaleString()));
    const level = element("td");
    level.append(
      element(
        "span",
        levels[item.level] || item.level,
        `log-level log-level-${item.level}`,
      ),
    );
    const module = element("td", item.component_title);
    module.append(element("small", groups.get(item.feature) || item.feature));
    const message = element("td", item.message, "log-message");
    if (item.kind === "diagnostic")
      message.append(element("small", "诊断事件"));
    const action = element("td");
    action.append(button("详情", () => showDetail(item)));
    row.append(level, module, message, action);
    rows.append(row);
  }
  if (!items.length) {
    const row = element("tr"),
      empty = element(
        "td",
        failure ? "日志暂时不可用，请重试。" : "当前筛选下暂无日志。",
        "empty",
      );
    empty.colSpan = 5;
    row.append(empty);
    rows.append(row);
  }
  $("logs-summary").textContent = data
    ? `当前筛选共 ${data.total} 条 · 本页 ${items.length} 条${data.debug ? " · 详细日志已开启" : " · 详细日志未开启"}`
    : "";
  $("logs-older").disabled = !data?.has_more || !items.length;
  $("logs-latest").disabled = !currentURL.searchParams.has("before");
  $("logs-page-note").textContent = currentURL.searchParams.has("before")
    ? "正在查看历史记录，自动刷新暂时停止。"
    : live
      ? "每 5 秒刷新一次"
      : "自动刷新已暂停";
}
function renderHealth() {
  renderDiagnostics($("logs-health-content"), {
    ...health,
    components: data?.components,
    feature: currentURL.searchParams.get("feature") || "",
    component: currentURL.searchParams.get("component") || "",
    query: currentURL.searchParams.get("q") || "",
  });
}
async function refresh() {
  if (!active || request) return;
  const controller = new AbortController();
  request = controller;
  $("logs-refresh").disabled = true;
  try {
    const options = {
      signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]),
    };
    const isHealth = currentURL.searchParams.get("tab") === "health";
    const results = await Promise.allSettled([
      api(endpoint(), options),
      isHealth
        ? api("/api/components/health", options)
        : Promise.resolve(health),
    ]);
    if (controller.signal.aborted || !active) return;
    const errors = [];
    if (results[0].status === "fulfilled") {
      data = results[0].value;
      filters();
    } else errors.push(`日志读取失败：${results[0].reason.message}`);
    if (results[1].status === "fulfilled") health = results[1].value;
    else errors.push(`组件健康读取失败：${results[1].reason.message}`);
    failure = errors.join("；");
    $("logs-notice").textContent = failure
      ? `${failure}。已保留上次读取结果。`
      : data?.warning
        ? `日志文件写入异常：${data.warning}`
        : "";
    $("logs-notice").className =
      `notice ${failure || data?.warning ? "error" : ""}`;
    renderRows();
    if (isHealth) renderHealth();
  } finally {
    if (request === controller) {
      request = null;
      $("logs-refresh").disabled = false;
    }
  }
}
function init() {
  tabKeyboard($("logs-tabs"));
  $("logs-tabs")
    .querySelectorAll("[data-tab]")
    .forEach((tab) =>
      tab.addEventListener("click", () => {
        const url = new URL(currentURL);
        url.searchParams.set("tab", tab.dataset.tab);
        url.searchParams.delete("before");
        void navigate(url.pathname + url.search);
      }),
    );
  $("logs-filters").addEventListener("submit", (event) => {
    event.preventDefault();
    apply();
  });
  for (const key of keys.filter((key) => key !== "q"))
    controls(key).addEventListener("change", () => {
      if (key === "feature") $("logs-component").value = "";
      apply();
    });
  $("logs-refresh").addEventListener("click", () => void refresh());
  $("logs-export").addEventListener("click", () => {
    $("logs-export").href = endpoint(true);
  });
  $("logs-live").addEventListener("click", () => {
    live = !live;
    $("logs-live").textContent = live ? "暂停刷新" : "恢复刷新";
    renderRows();
    if (live) void refresh();
  });
  $("logs-older").addEventListener("click", () => {
    const last = data?.items?.at(-1);
    if (!last) return;
    const url = new URL(currentURL);
    url.searchParams.set("before", String(last.id));
    void navigate(url.pathname + url.search);
  });
  $("logs-latest").addEventListener("click", () => {
    const url = new URL(currentURL);
    url.searchParams.delete("before");
    void navigate(url.pathname + url.search);
  });
}
async function load(url) {
  stop();
  active = true;
  currentURL = new URL(url);
  const version = generation;
  const tab =
    currentURL.searchParams.get("tab") === "health" ? "health" : "runtime";
  activateTabs($("logs-tabs"), tab);
  $("logs-runtime").hidden = tab !== "runtime";
  $("logs-health").hidden = tab !== "health";
  $("logs-export").hidden = tab !== "runtime";
  document
    .querySelectorAll("[data-log-filter]")
    .forEach((label) => (label.hidden = tab !== "runtime"));
  filters();
  for (const key of keys.filter(
    (key) => key !== "feature" && key !== "component",
  ))
    controls(key).value = currentURL.searchParams.get(key) || "";
  $("logs-export").href = endpoint(true);
  data = data ? { ...data, items: [], total: 0, has_more: false } : null;
  renderRows();
  await refresh();
  if (active && version === generation)
    timer = setInterval(() => {
      if (live && !document.hidden && !currentURL.searchParams.has("before"))
        void refresh();
    }, 5000);
}
function stop() {
  ++generation;
  active = false;
  clearInterval(timer);
  request?.abort();
  request = null;
}
export const page = { init, load, stop };

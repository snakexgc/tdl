import { api } from "./api.js";
import { navigate } from "./router.js";
import {
  element,
  button,
  tabKeyboard,
  activateTabs,
  closeDrawer,
} from "./ui.js";
import {
  ConfigurationDrafts,
  settingsTabs,
  settingsGroups,
  searchSettings,
  pendingSettings,
  changeValue,
} from "./settings-model.js";
import { renderSettingsBlock } from "./settings-fields.js";
import { renderSystem } from "./settings-system.js";

let store,
  data,
  runtimeDraft,
  configData,
  activeTab = settingsTabs[0][0],
  mounted = new Map(),
  blocks = [],
  busy = new Set(),
  restarting = false,
  reloadOnEntry = false;
const $ = (id) => document.getElementById(id);
const dirty = () => Boolean(store?.dirty || runtimeDraft !== undefined);
const message = (text, kind = "") => {
  $("settings-message").textContent = text;
  $("settings-message").className = `notice ${kind}`;
};

async function init() {
  mounted = new Map();
  blocks = [];
  busy = new Set();
  runtimeDraft = undefined;
  restarting = false;
  reloadOnEntry = false;
  const tabs = $("settings-tabs");
  for (const [id, title] of settingsTabs) {
    const tab = button(
      title,
      () => navigate(`/config?tab=${id}`),
      "tab-button",
    );
    tab.dataset.tab = id;
    tab.id = `settings-tab-${id}`;
    tab.setAttribute("role", "tab");
    tab.setAttribute("aria-controls", `settings-${id}`);
    const badge = element("span", null, "settings-tab-count");
    badge.hidden = true;
    badge.setAttribute("aria-hidden", "true");
    tab.append(badge);
    tabs.append(tab);
    const panel = element("section", null, "settings-panel");
    panel.id = `settings-${id}`;
    panel.hidden = true;
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", tab.id);
    $("settings-panels").append(panel);
  }
  tabKeyboard(tabs);
  $("settings-reload").addEventListener("click", () => {
    void reload().catch((error) => message(error.message, "error"));
  });
  $("settings-restart").addEventListener("click", restart);
  $("settings-search").addEventListener("input", renderSearch);
  $("settings-search").addEventListener("keydown", (event) => {
    if (event.key === "Escape") {
      event.preventDefault();
      $("settings-search").value = "";
      renderSearch();
    }
  });
  await readData();
  store = new ConfigurationDrafts(data.components || []);
  $("settings-readonly").hidden = Boolean(data.editable);
}
async function readData() {
  [data, configData] = await Promise.all([
    api("/api/components"),
    api("/api/config"),
  ]);
}
async function reload() {
  if (busy.size) {
    message("正在处理，请稍后刷新。");
    return;
  }
  busy.add("reload");
  updateDraft();
  try {
    await Promise.allSettled([...mounted.values()]);
    closeDrawer();
    await readData();
    store = new ConfigurationDrafts(data.components || []);
    runtimeDraft = undefined;
    $("settings-restart-message").textContent = "";
    $("settings-readonly").hidden = Boolean(data.editable);
    renderSearch();
    await redrawPanels();
    message("已从 tdl_config.json 重新读取配置。", "success");
  } finally {
    busy.delete("reload");
    updateDraft();
  }
}
async function redrawPanels() {
  for (const block of blocks) block.dispose?.();
  blocks = [];
  mounted.clear();
  $("settings-panels")
    .querySelectorAll(".settings-panel")
    .forEach((panel) => panel.replaceChildren());
  await display(activeTab);
}
async function load(url) {
  if (reloadOnEntry) {
    reloadOnEntry = false;
    await reload();
  }
  const requested = url?.searchParams?.get("tab");
  activeTab = settingsTabs.some(([id]) => id === requested)
    ? requested
    : settingsTabs[0][0];
  await display(activeTab);
  const target =
    url?.hash && document.getElementById(decodeURIComponent(url.hash.slice(1)));
  if (target && !target.closest("[hidden]")) {
    for (let parent = target; parent; parent = parent.parentElement)
      if (parent.tagName === "DETAILS") parent.open = true;
    target.scrollIntoView({ block: "start" });
    target.focus({ preventScroll: true });
  }
}
async function display(tab) {
  activeTab = tab;
  activateTabs($("settings-tabs"), tab);
  for (const [id] of settingsTabs) $(`settings-${id}`).hidden = id !== tab;
  await ensureMounted(tab);
  updateDraft();
}
async function ensureMounted(tab) {
  if (!mounted.has(tab)) {
    const promise = mountTab(tab);
    mounted.set(tab, promise);
    try {
      await promise;
    } catch (error) {
      mounted.delete(tab);
      throw error;
    }
  } else await mounted.get(tab);
}
function updateDraft() {
  const count = (store?.count || 0) + Number(runtimeDraft !== undefined);
  $("settings-draft-status").textContent = count
    ? `${count} 项修改尚未保存，可在顶部保存并重启。切换分类会保留输入。`
    : "没有未保存的输入。";
  $("settings-draft-status").classList.toggle("has-drafts", count > 0);
  $("settings-reload").disabled = busy.size > 0;
  $("settings-reload").textContent = busy.has("reload") ? "正在刷新…" : "刷新";
  for (const [tab, title] of settingsTabs) {
    const count =
      settingsGroups([...store.components.values()], tab).reduce(
        (sum, group) =>
          sum +
          Object.keys(
            store.patch(
              group.id,
              group.fields.map((field) => field.name),
            ),
          ).length,
        0,
      ) + Number(tab === "system" && runtimeDraft !== undefined);
    const node = $(`settings-tab-${tab}`),
      badge = node.querySelector(".settings-tab-count");
    badge.textContent = String(count);
    badge.hidden = !count;
    node.setAttribute(
      "aria-label",
      count ? `${title}，${count} 项未保存` : title,
    );
  }
  for (const block of blocks) block.sync();
  renderPending();
}

function renderPending() {
  const changes = pendingSettings(
    data?.components || [],
    configData,
    store,
    runtimeDraft,
  );
  $("settings-pending").hidden = !changes.length && !restarting;
  const host = $("settings-changes");
  host.replaceChildren();
  for (const change of changes) {
    const row = element("tr"),
      name = element("th"),
      title = element("a", change.title);
    name.scope = "row";
    title.href =
      change.name === "enabled"
        ? "/modules"
        : `/config?tab=${change.tab}#${encodeURIComponent(`setting-${change.id}-${change.name}`)}`;
    title.dataset.appLink = "";
    name.append(title, element("small", change.section, "subtle"));
    name.append(
      element("small", change.draft ? "尚未保存" : "已保存，待重启", "subtle"),
    );
    row.append(name);
    for (const side of ["before", "after"]) {
      const cell = element("td");
      cell.dataset.label = side === "before" ? "当前运行值" : "修改后的值";
      cell.append(element("pre", changeValue(change, side)));
      row.append(cell);
    }
    host.append(row);
  }
  $("settings-changes-table").hidden = !changes.length;
  $("settings-pending-status").textContent = restarting
    ? "重启请求已提交。服务恢复后重新打开页面，新配置生效后此清单会清空。"
    : `${changes.length} 项修改尚未应用。${dirty() ? "点击“保存并重启”将保存全部分类中的修改并重启。" : "修改已写入配置文件，点击“保存并重启”后生效。"}重启会暂时中断连接和任务。${configData?.restart_available ? "" : "当前运行方式不支持通过面板重启，请先保存区块，再手动重启 TDL。"}`;
  $("settings-restart").disabled =
    !configData?.restart_available ||
    !data?.editable ||
    busy.size > 0 ||
    !changes.length ||
    restarting;
  $("settings-restart").textContent = restarting ? "正在重启…" : "保存并重启";
}

async function restart() {
  if ($("settings-restart").disabled) return;
  message("");
  const status = $("settings-restart-message");
  busy.add("validate");
  updateDraft();
  status.textContent = "正在检查并保存修改…";
  status.className = "notice";
  let saved = false;
  let wrote = false;
  try {
    await Promise.all([...mounted.values()]);
    for (const [tab] of settingsTabs) {
      if (
        settingsGroups([...store.components.values()], tab).some(
          (group) =>
            Object.keys(
              store.patch(
                group.id,
                group.fields.map((field) => field.name),
              ),
            ).length,
        )
      )
        await ensureMounted(tab);
    }
    for (const block of blocks) {
      const invalid = block.validate?.();
      if (!invalid) continue;
      await navigate(`/config?tab=${block.tab}`);
      invalid.scrollIntoView({ block: "center" });
      invalid.reportValidity();
      throw new Error("请先修正标出的设置，修改尚未保存。");
    }
    busy.add("runtime");
    updateDraft();
    for (const [id, fields] of [...store.drafts]) {
      await persistComponent(id, Object.fromEntries(fields));
      wrote = true;
    }
    if (runtimeDraft !== undefined) {
      configData = await api("/api/config", {
        method: "PATCH",
        body: JSON.stringify({ values: { debug: runtimeDraft } }),
      });
      runtimeDraft = undefined;
      wrote = true;
    }
    saved = true;
    await redrawPanels();
    status.textContent = "配置已保存，正在提交重启请求…";
    await api("/api/system/reboot", { method: "POST", body: "{}" });
    restarting = true;
    status.textContent =
      "重启请求已提交。若修改了面板地址或登录凭据，请使用新地址或凭据重新登录。";
  } catch (error) {
    const detail =
      error.status === 409
        ? "配置文件已变化，请核对后刷新再修改。"
        : error.message;
    status.textContent = saved
      ? `重启失败：${error.message}。配置已保存，可稍后重试。`
      : `未执行重启：${detail} 未保存的输入仍保留，已写入文件的修改也会保留。`;
    status.className = "notice error";
    message(status.textContent, "error");
    if (!saved && wrote) await redrawPanels();
  } finally {
    busy.delete("validate");
    if (!restarting) busy.delete("runtime");
    updateDraft();
  }
}
async function mountTab(tab) {
  const host = $(`settings-${tab}`);
  host.replaceChildren();
  const intro = element("div", null, "settings-intro");
  intro.append(
    element("p", settingsTabs.find(([id]) => id === tab)[2], "subtle"),
  );
  const related = {
    account: ["/user", "登录与切换账号"],
    notifications: ["/config?tab=bot", "设置机器人"],
    download: ["/config?tab=links", "设置下载链接地址"],
  }[tab];
  if (related) {
    const link = element("a", related[1]);
    link.href = related[0];
    link.dataset.appLink = "";
    intro.append(link);
  }
  host.append(intro);
  if (tab === "system") {
    const block = await renderSystem(host, {
      store,
      data,
      configData,
      busy,
      onChanged: (result) => {
        configData = result;
        renderPending();
        window.dispatchEvent(new Event("configuration-changed"));
      },
      getDraft: () => runtimeDraft,
      onResetAccepted: discardDrafts,
      markDirty: (value) => {
        runtimeDraft = value;
        updateDraft();
      },
      setBusy: (value) => {
        if (value) busy.add("runtime");
        else busy.delete("runtime");
        updateDraft();
      },
    });
    blocks.push({ ...block, tab });
  }
  const groups = settingsGroups([...store.components.values()], tab);
  for (const group of groups) {
    const slot = element("div");
    host.append(slot);
    const block = await renderSettingsBlock(slot, group, {
      store,
      editable: data.editable,
      busy,
      update: updateDraft,
      save: saveBlock,
    });
    blocks.push({ ...block, tab });
  }
  if (!groups.length && tab !== "system")
    host.append(element("p", "此分类暂无可配置项目。", "empty"));
}
async function saveBlock(group, report, redraw) {
  const id = group.id,
    names = group.fields.map((field) => field.name),
    values = store.patch(id, names);
  if (!data.editable || busy.size > 0 || !Object.keys(values).length) return;
  busy.add(id);
  updateDraft();
  try {
    await persistComponent(id, values);
    message("");
    await redraw();
    report(
      "已保存到 tdl_config.json。请在顶部确认修改，重启后生效。",
      "success",
    );
    window.dispatchEvent(
      new CustomEvent("configuration-changed", { detail: { id } }),
    );
  } catch (error) {
    report(
      error.status === 409
        ? "配置文件已变化，输入仍保留。请核对后点击“刷新”，再重新修改并保存。"
        : error.message,
      "error",
    );
  } finally {
    busy.delete(id);
    updateDraft();
  }
}
async function persistComponent(id, values) {
  const result = await api("/api/components", {
    method: "PATCH",
    body: JSON.stringify({
      id,
      values,
      revision: store.components.get(id).revision || "",
    }),
  });
  const saved = result.components.find((component) => component.id === id);
  if (!saved) throw new Error("保存响应缺少组件信息，请刷新核对。");
  store.accept(saved, Object.keys(values));
  data.components = result.components;
}
function discardDrafts() {
  store?.drafts.clear();
  runtimeDraft = undefined;
  updateDraft();
}
function renderSearch() {
  const query = $("settings-search").value.trim(),
    host = $("settings-search-results"),
    list = $("settings-search-list");
  host.hidden = !query;
  list.replaceChildren();
  if (!query || !store) return;
  const matches = searchSettings([...store.components.values()], query);
  $("settings-search-count").textContent = matches.length
    ? `找到 ${matches.length} 项设置，点击可定位到对应字段。`
    : "没有找到匹配的设置。试试「目录」「端口」，或输入配置字段名。";
  for (const match of matches) {
    const item = element("li"),
      link = element("a");
    link.href = `/config?tab=${match.tab}#${encodeURIComponent(match.target)}`;
    link.dataset.appLink = "";
    link.append(
      element("strong", match.title),
      element("span", `${match.category} / ${match.section}`, "subtle"),
    );
    link.addEventListener("click", (event) => {
      if (
        event.button ||
        event.ctrlKey ||
        event.metaKey ||
        event.shiftKey ||
        event.altKey
      )
        return;
      $("settings-search").value = "";
      host.hidden = true;
    });
    item.append(link);
    list.append(item);
  }
}
function beforeLeave() {
  if (busy.size) {
    message("正在保存，请等待完成后再离开。");
    return false;
  }
  if (dirty() && !confirm("设置尚未保存，离开将撤销这些修改。是否继续？"))
    return false;
  if (dirty()) {
    store.drafts.clear();
    runtimeDraft = undefined;
  }
  reloadOnEntry = true;
  return true;
}
function stop() {
  closeDrawer();
}
export const page = { init, load, stop, dirty, beforeLeave };

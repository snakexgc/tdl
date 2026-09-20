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
  reloadOnEntry = false;
const $ = (id) => document.getElementById(id);
const dirty = () =>
  Boolean(store?.dirty || runtimeDraft !== undefined);
const message = (text, kind = "") => {
  $("settings-message").textContent = text;
  $("settings-message").className = `notice ${kind}`;
};

async function init() {
  mounted = new Map();
  blocks = [];
  busy = new Set();
  runtimeDraft = undefined;
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
  await readData();
  store = new ConfigurationDrafts(data.components || []);
}
async function readData() {
  [data, configData] = await Promise.all([
    api("/api/components"),
    api("/api/config"),
  ]);
}
async function reload() {
  if (busy.size) {
    message("正在保存，请稍后重新读取。");
    return;
  }
  busy.add("reload");
  try {
    await Promise.allSettled([...mounted.values()]);
    closeDrawer();
    await readData();
    store.rebase(data.components || []);
    for (const block of blocks) block.dispose?.();
    blocks = [];
    mounted.clear();
    $("settings-panels")
      .querySelectorAll(".settings-panel")
      .forEach((panel) => panel.replaceChildren());
    await display(activeTab);
    message(
      dirty()
        ? "已读取服务端最新值，草稿已保留。请核对标注的服务端值后再保存。"
        : "设置已重新读取。",
      "success",
    );
  } finally {
    busy.delete("reload");
    updateDraft();
  }
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
    if (target.tagName === "DETAILS") target.open = true;
    target.scrollIntoView({ block: "start" });
  }
}
async function display(tab) {
  activeTab = tab;
  activateTabs($("settings-tabs"), tab);
  for (const [id] of settingsTabs) $(`settings-${id}`).hidden = id !== tab;
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
  updateDraft();
}
function updateDraft() {
  const count = (store?.count || 0) + Number(runtimeDraft !== undefined);
  $("settings-draft-status").textContent = count
    ? `${count} 个字段尚未保存。切换设置标签会保留草稿。`
    : "";
  for (const block of blocks) block.sync();
}
async function mountTab(tab) {
  const host = $(`settings-${tab}`);
  host.replaceChildren();
  if (tab === "system")
    await renderSystem(host, {
      store,
      data,
      configData,
      onChanged: () => {
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
  const groups = settingsGroups([...store.components.values()], tab);
  for (const group of groups) {
    const slot = element("div");
    if (tab === "system" && group === groups[0]) slot.id = "advanced";
    host.append(slot);
    const block = await renderSettingsBlock(slot, group, {
      store,
      editable: data.editable,
      busy,
      update: updateDraft,
      save: saveBlock,
    });
    blocks.push(block);
  }
  if (!host.children.length)
    host.append(element("p", "此分类暂无可配置项目。", "empty"));
}
async function saveBlock(group, report, redraw) {
  const id = group.id,
    names = group.fields.map((field) => field.name),
    values = store.patch(id, names);
  if (busy.has(id) || !Object.keys(values).length) return;
  busy.add(id);
  updateDraft();
  try {
    const result = await api("/api/components", {
      method: "PATCH",
      body: JSON.stringify({
        id,
        values,
        revision: store.components.get(id).revision || "",
      }),
    });
    const saved = result.components.find((component) => component.id === id);
    if (!saved) throw new Error("保存响应缺少组件信息，请重新读取核对。");
    store.accept(saved, Object.keys(values));
    await redraw();
    report(
      saved.pending_restart
        ? "已保存，正在重新配置对应服务。"
        : "更改已保存并应用。",
      "success",
    );
    window.dispatchEvent(
      new CustomEvent("configuration-changed", { detail: { id } }),
    );
  } catch (error) {
    report(
      error.status === 409
        ? "服务端配置已变化，草稿已保留。请点击「重新读取 · 保留草稿」，核对后再次保存。"
        : error.message,
      "error",
    );
  } finally {
    busy.delete(id);
    updateDraft();
  }
}
function discardDrafts() {
  store?.drafts.clear();
  runtimeDraft = undefined;
  updateDraft();
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

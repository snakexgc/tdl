import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

async function load(relative) {
  const module = new vm.SourceTextModule(await readFile(new URL(relative, import.meta.url), "utf8"), { context: vm.createContext({ structuredClone }) });
  await module.link(() => { throw new Error("Model should not require DOM or network"); }); await module.evaluate(); return module.namespace;
}
const models = await load("../../application/panel.webui/assets/static/js/settings-model.js");
const component = (revision = "r1", values = {}) => ({ id: "trigger", revision, fields: [
  { name: "download", type: "strings", default: [], settings_tab: "download", settings_section: "表情" },
  { name: "forward", type: "strings", default: [], settings_tab: "forward", settings_section: "表情" },
  { name: "secret", type: "string", secret: true, default: "", settings_tab: "system" },
], values: { download: ["old"], forward: ["old"], ...values } });
const plain = value => JSON.parse(JSON.stringify(value));

test("settings patches are scoped and share the latest revision across tabs", () => {
  const store = new models.ConfigurationDrafts([component()]);
  store.set("trigger", "download", ["new download"]); store.set("trigger", "forward", ["new forward"]);
  assert.deepEqual(plain(store.patch("trigger", ["download"])), { download: ["new download"] });
  store.accept(component("r2", { download: ["new download"] }), ["download"]);
  assert.equal(store.components.get("trigger").revision, "r2");
  assert.deepEqual(plain(store.patch("trigger", ["forward"])), { forward: ["new forward"] });
  assert.equal(store.count, 1);
});
test("conflict re-read retains drafts, adopts server revision and preserves secret semantics", () => {
  const store = new models.ConfigurationDrafts([component()]);
  store.set("trigger", "forward", ["mine"]); store.set("trigger", "secret", "");
  assert.equal(store.count, 1);
  store.rebase([component("r3", { forward: ["theirs"] })]);
  assert.deepEqual(plain(store.value("trigger", "forward")), ["mine"]);
  assert.deepEqual(plain(store.baseline("trigger", "forward")), ["theirs"]);
  assert.equal(store.components.get("trigger").revision, "r3");
  store.set("trigger", "secret", "new secret");
  store.accept(component("r4"), ["secret"]);
  assert.equal(store.value("trigger", "secret"), "");
  assert.deepEqual(plain(store.value("trigger", "forward")), ["mine"]);
  store.reset("trigger", ["forward"]); assert.equal(store.dirty, false);
});
test("each field has one settings location including disabled and unknown components", () => {
  const known = component(); known.enabled = false;
  const unknown = { id: "extension", fields: [{ name: "custom", settings_tab: "unrecognized" }] };
  const groups = models.settingsTabs.flatMap(([tab]) => models.settingsGroups([known, unknown], tab));
  const fields = groups.flatMap(group => group.fields.map(field => group.id + "/" + field.name));
  assert.equal(fields.length, 4); assert.equal(new Set(fields).size, 4);
  assert.equal(models.fieldTab(unknown.fields[0]), "system");
});

test("obsolete per-service proxies do not create additional editors", () => {
  const inputs = [
    { id: "account.telegram", fields: [{ name: "proxy", settings_tab: "network", settings_section: "网络代理" }] },
    { id: "console.bot", fields: [{ name: "proxy", settings_tab: "bot", replaced_by: "account.telegram.proxy" }] },
    { id: "update.self", fields: [{ name: "proxy", settings_tab: "system", replaced_by: "account.telegram.proxy" }] },
  ];
  const groups = models.settingsTabs.flatMap(([tab]) => models.settingsGroups(inputs, tab));
  assert.deepEqual(plain(groups.map(group => group.id)), ["account.telegram"]);
});

const modules = await load("../../application/panel.webui/assets/static/js/module-model.js");
test("functional module groups preserve every component and distinguish enablement from health", () => {
  const items = [
    { id: "worker", feature: { id: "download", title: "下载管理", order: 20 }, enabled: true, state: "blocked" },
    { id: "controller", feature: { id: "download", title: "下载管理", order: 20 }, enabled: false, state: "running" },
    { id: "account", feature: { id: "account", title: "账号管理", order: 10 }, enabled: true, state: "running", pending_restart: true },
    { id: "extension", enabled: true, state: "failed", error: "failure" },
  ];
  const groups = modules.featureGroups(items);
  assert.deepEqual(plain(groups.map(group => group.id)), ["account", "download", "extensions"]);
  assert.equal(groups.flatMap(group => group.components).length, 4);
  assert.equal(modules.componentStatus(items[0]).text, "依赖未就绪");
  assert.equal(modules.componentStatus(items[1]).text, "已停用");
  assert.equal(modules.componentStatus(items[2]).text, "等待重启");
  assert.equal(modules.componentStatus(items[3]).kind, "error");
});
const downloads = await load("../../application/download.control/assets/static/js/download-model.js");
test("task IDs cannot collide across executors or accounts and batches stay scoped", () => {
  const tasks = [{ account: "a", executor: "local", id: "same", state: "paused" }, { account: "a", executor: "aria2", id: "same", state: "paused" }, { account: "b", executor: "aria2", id: "same", state: "complete" }];
  assert.equal(new Set(tasks.map(downloads.taskKey)).size, 3);
  const groups = plain(downloads.groupActions([...tasks, tasks[0]], "resume"));
  assert.deepEqual(groups, [
    { account: "a", executor: "local", action: "resume", ids: ["same"] },
    { account: "a", executor: "aria2", action: "resume", ids: ["same"] },
  ]);
  assert.equal(downloads.groupActions(tasks, "delete").length, 3);
  assert.equal(downloads.eligible({executor: "aria2", state: "error"}, "resume"), false, "terminated aria2 results cannot be unpaused");
  assert.equal(downloads.eligible({executor: "local", state: "error"}, "resume"), true);
});

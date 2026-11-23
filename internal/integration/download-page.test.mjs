import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("download page queries only the selected downloader and shows local storage", async () => {
  class Element {
    constructor(text = "") { this.textContent = text; this.children = []; this.events = {}; this.value = ""; }
    append(...items) { this.children.push(...items); if (!this.value) this.value = items[0]?.value || ""; }
    replaceChildren(...items) { this.children = items; this.value = ""; }
    querySelectorAll() { return []; }
    closest() { return this; }
    addEventListener(name, action) { this.events[name] = action; }
  }
  const nodes = new Map(), requests = [], subscriptions = new Map();
  const node = id => { if (!nodes.has(id)) nodes.set(id, new Element()); return nodes.get(id); };
  let mode = "local";
  const context = vm.createContext({ URL, URLSearchParams, AbortSignal,
    location: new URL("http://panel.test/downloads"),
    document: { getElementById: node },
  });
  const definitions = {
    "./api.js": { api: async url => {
      requests.push(url);
      if (url === "/api/status") return { downloader: { mode, aria2_enabled: mode === "aria2" } };
      if (url === "/api/download-storage") return { root: "/downloads", exists: true, free_bytes: 400, total_bytes: 1000, file_bytes: 100, file_count: 2 };
      return { items: [] };
    } },
    "./events.js": { observe: (topic, callback) => { subscriptions.set(topic, callback); return () => subscriptions.delete(topic); } },
    "./router.js": { navigate() {} },
    "./utils.js": { escapeHTML: String, escapeAttr: String, formatBytes: n => `${n} B`, formatTime: String },
    "./ui.js": { element: (_tag, text) => new Element(text), fragment() {}, openDrawer() {}, closeDrawer() {}, tabKeyboard() {}, activateTabs() {} },
  };
  const model = new vm.SourceTextModule(await readFile(new URL("../../application/download.control/assets/static/js/download-model.js", import.meta.url), "utf8"), { context });
  await model.link(() => { throw new Error("unexpected model import"); });
  const shared = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/state.js", import.meta.url), "utf8"), { context });
  await shared.link(() => { throw new Error("unexpected state import"); });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/download.control/assets/static/js/downloads.js", import.meta.url), "utf8"), { context });
  await module.link(name => name === "./download-model.js" ? model : name === "./state.js" ? shared : new vm.SyntheticModule(Object.keys(definitions[name]), function () {
    for (const [key, value] of Object.entries(definitions[name])) this.setExport(key, value);
  }, { context }));
  await module.evaluate();
  const { page } = module.namespace;
  page.init();
  await page.load(new URL("http://panel.test/downloads"));
  assert(requests.includes("/api/download-tasks?executor=local"));
  assert(!requests.some(url => url.includes("aria2")));
  assert.deepEqual([...subscriptions.keys()].sort(), ["download-storage", "download-tasks-local"]);
  assert.equal(node("advanced-download").hidden, true);
  assert.equal(node("download-storage").hidden, false);
  assert.equal(node("download-storage-free").textContent, "400 B");
  assert.equal(node("download-storage-bytes").textContent, "100 B");
  assert.equal(node("download-storage-count").textContent, "2");
  assert.match(node("download-body").innerHTML, /暂无下载任务/);
  node("download-search").value = "missing";
  node("download-search").events.input();
  assert.match(node("download-body").innerHTML, /清除筛选/);
  node("download-search").value = "";
  node("download-search").events.input();
  assert.equal(node("download-storage-alert").hidden, true);
  subscriptions.get("download-storage")(null, "permission denied");
  assert.equal(node("download-storage-free").textContent, "—");
  assert.match(node("download-storage-message").textContent, /permission denied/);
  assert.equal(node("download-storage-alert").hidden, false);
  requests.length = 0;
  mode = "aria2";
  await page.load(new URL("http://panel.test/downloads"));
  assert(!requests.includes("/api/download-storage"));
  assert(!requests.includes("/api/download-tasks?executor=local"));
  assert.deepEqual([...subscriptions.keys()], ["download-tasks-aria2"]);
  assert.equal(node("advanced-download").hidden, false);
  assert.equal(node("download-storage").hidden, true);
  requests.length = 0;
  mode = "http";
  await page.load(new URL("http://panel.test/downloads"));
  assert(!requests.some(url => url.includes("/api/download-tasks")));
  assert.match(node("download-body").innerHTML, /HTTP 直链/);
  page.stop();
  assert.equal(subscriptions.size, 0);
});

test("link history offers direct links without offering an unavailable downloader", async () => {
  const nodes = new Map();
  const node = (id) => {
    if (!nodes.has(id)) nodes.set(id, { querySelectorAll: () => [], addEventListener() {}, setAttribute() {} });
    return nodes.get(id);
  };
  const item = { id: "record", file_name: "example.zip", url: "https://example.test/file" };
  const context = vm.createContext({ document: { getElementById: node, querySelectorAll: () => [] } });
  const shared = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/state.js", import.meta.url), "utf8"), { context });
  await shared.link(() => { throw new Error("unexpected state import"); });
  await shared.evaluate();
  const { state } = shared.namespace;
  Object.assign(state, {
    downloader: { mode: "http" }, downloaderMode: "http", kvItems: [],
    selectedKV: new Set([item.id]), kvSort: { field: "created_at", dir: "desc" },
  });
  const definitions = {
    "./api.js": { api: async () => ({ items: [item] }) },
    "./utils.js": { collator: new Intl.Collator(), escapeHTML: String, escapeAttr: String, formatBytes: String, formatTime: String },
    "./download-model.js": { statusClass: String, statusLabel: String },
    "./status.js": { loadStatus: async () => {} },
    "./ui.js": { element() {}, openDrawer() {} },
    "./storage-maintenance.js": { cleanStorage() {} },
  };
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/kv.js", import.meta.url), "utf8"), { context });
  await module.link(name => name === "./state.js" ? shared : new vm.SyntheticModule(Object.keys(definitions[name]), function () {
    for (const [key, value] of Object.entries(definitions[name])) this.setExport(key, value);
  }, { context }));
  await module.evaluate();

  await module.namespace.page.load();
  assert.match(node("kv-body").innerHTML, /href="https:\/\/example.test\/file"/);
  assert.doesNotMatch(node("kv-body").innerHTML, /data-download-link/);
  assert.equal(node("download-selected").hidden, true);
  assert.equal(node("delete-selected").hidden, false);

  state.downloader = { mode: "aria2", aria2_enabled: false };
  state.downloaderMode = "aria2";
  await module.namespace.page.load();
  assert.doesNotMatch(node("kv-body").innerHTML, /data-download-link/);
  assert.equal(node("download-selected").hidden, true);

  state.downloader = { mode: "local" };
  state.downloaderMode = "local";
  await module.namespace.page.load();
  assert.match(node("kv-body").innerHTML, /data-download-link="record"/);
  assert.match(node("download-selected").textContent, /加入下载队列/);
  assert.equal(node("download-selected").hidden, false);
});

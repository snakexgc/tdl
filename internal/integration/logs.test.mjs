import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("log navigation cancels stale reads, retains data on failure and stops polling on leave", async () => {
  class Element {
    constructor(text = "") { this.textContent = text; this.children = []; this.events = {}; this.value = ""; }
    append(...items) { this.children.push(...items); }
    replaceChildren(...items) { this.children = items; }
    querySelectorAll() { return []; }
    addEventListener(name, action) { this.events[name] = action; }
  }
  const elements = new Map(), requests = [], timers = new Map(); let sequence = 0;
  const node = id => { if (!elements.has(id)) elements.set(id, new Element()); return elements.get(id); };
  const context = vm.createContext({ URL, URLSearchParams, AbortController, AbortSignal, console,
    document: { getElementById: node, querySelectorAll: () => [], hidden: false },
    setInterval: callback => { timers.set(++sequence, callback); return sequence; }, clearInterval: id => timers.delete(id),
  });
  const definitions = {
    "./api.js": { api: (url, options) => new Promise((resolve, reject) => requests.push({ url, options, resolve, reject })) },
    "./router.js": { navigate: async () => {} },
    "./ui.js": { element: (_tag, text) => new Element(text), button: text => new Element(text), openDrawer() {}, tabKeyboard() {}, activateTabs() {} },
    "./module-model.js": { featureGroups: () => [] },
    "./module-diagnostics.js": { renderDiagnostics() {} },
  };
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/logs.js", import.meta.url), "utf8"), { context });
  await module.link(name => new vm.SyntheticModule(Object.keys(definitions[name]), function () { for (const [key, value] of Object.entries(definitions[name])) this.setExport(key, value); }, { context }));
  await module.evaluate();
  const page = module.namespace.page; page.init();
  const snapshot = message => ({ items: [{ id: 1, at: new Date().toISOString(), level: "info", component_title: "Local", message }], total: 1, components: [] });
  const first = page.load(new URL("http://panel.test/logs?component=downloader.local"));
  const second = page.load(new URL("http://panel.test/logs?component=forwarder"));
  assert(requests[0].options.signal.aborted);
  requests[0].resolve(snapshot("stale")); await first;
  assert.equal(timers.size, 0, "a superseded load must not start another timer");
  assert.equal(node("logs-rows").children[0].children[0].textContent, "当前筛选下暂无日志。");
  requests[1].resolve(snapshot("current")); await second;
  assert.equal(timers.size, 1);
  assert(requests[1].url.includes("component=forwarder"));
  assert.equal(node("logs-rows").children[0].children[3].textContent, "current");
  timers.values().next().value(); requests.at(-1).reject(new Error("offline")); await new Promise(setImmediate);
  assert.match(node("logs-notice").textContent, /offline/);
  assert.equal(node("logs-rows").children[0].children[3].textContent, "current", "failed refresh retains previous results");
  timers.values().next().value(); const pending = requests.at(-1); page.stop();
  assert(pending.options.signal.aborted); assert.equal(timers.size, 0);
  pending.resolve(snapshot("late after leave")); await new Promise(setImmediate);
  assert.equal(node("logs-rows").children[0].children[3].textContent, "current");
});

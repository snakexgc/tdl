import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const assets = new URL("../../application/panel.webui/assets/", import.meta.url);
const source = file => readFile(new URL(file, assets), "utf8");

async function mountDashboard(mode = "local") {
  const html = await source("views/dashboard.html");
  const nodes = new Map();
  for (const [, id] of html.matchAll(/\bid="([^"]+)"/g)) {
    nodes.set(id, {
      textContent: "", innerHTML: "", open: false, attributes: {}, listeners: {},
      setAttribute(name, value) { this.attributes[name] = value; },
      removeAttribute(name) { delete this.attributes[name]; },
      toggleAttribute(name, value) { if (value) this.attributes[name] = ""; else this.removeAttribute(name); },
      addEventListener(name, callback) { this.listeners[name] = callback; },
      getBoundingClientRect() { return { width: 340, height: 130, left: 0 }; },
      querySelector() { return {}; },
    });
  }
  const subscriptions = new Map();
  const requests = [];
  const start = Date.parse("2026-11-19T12:00:00Z");
  const sample = (seconds, fields = {}) => ({
    sampled_at: new Date(start + seconds * 1000).toISOString(),
    process: { cpu_percent: 10, goroutines: 8 },
    memory: { total_bytes: 1024, software_bytes: 900, heap_retained_idle_bytes: 124 },
    download: { gotd_speed_bps: 64, gotd_bytes_total: 64000 },
    ...fields,
  });
  const context = vm.createContext({ document: { getElementById: id => nodes.get(id) }, window: new EventTarget(), AbortSignal });
  const definitions = {
    "./api.js": { api: async url => {
      requests.push(url);
      if (url === "/api/status") return { downloader: { mode, aria2_enabled: mode === "aria2" } };
      if (url === "/api/dashboard") return sample(0);
      return { items: [] };
    } },
    "./events.js": { observe: (topic, callback) => {
      subscriptions.set(topic, callback);
      return () => subscriptions.delete(topic);
    } },
    "./status.js": { currentDownloader: async () => ({ mode, aria2_enabled: mode === "aria2" }) },
  };
  const utils = new vm.SourceTextModule(await source("static/js/utils.js"), { context });
  await utils.link(() => { throw new Error("unexpected utility import"); });
  const shared = new vm.SourceTextModule(await source("static/js/state.js"), { context });
  await shared.link(() => { throw new Error("unexpected state import"); });
  const module = new vm.SourceTextModule(await source("static/js/dashboard.js"), { context });
  await module.link(name => name === "./utils.js" ? utils : name === "./state.js" ? shared : new vm.SyntheticModule(Object.keys(definitions[name]), function () {
    for (const [key, value] of Object.entries(definitions[name])) this.setExport(key, value);
  }, { context }));
  await module.evaluate();
  const { page } = module.namespace;
  page.init();
  await page.load();
  return { page, nodes, requests, subscriptions, samples: shared.namespace.state.dashboardSamples, sample };
}

test("dashboard shows the selected downloader, real failures and executor availability", async () => {
  for (const mode of ["local", "aria2", "http"]) {
    const { nodes, requests, subscriptions, page } = await mountDashboard(mode);
    assert.deepEqual(requests.filter(url => url.startsWith("/api/download-tasks")),
      mode === "http" ? [] : [`/api/download-tasks?executor=${mode}`]);
    if (mode === "http") {
      assert.equal(nodes.get("overview-downloads").textContent, "—");
      assert.match(nodes.get("overview-download-errors-note").textContent, /未启用/);
    } else {
      const receive = subscriptions.get(`download-tasks-${mode}`);
      receive({ items: [{ state: "complete" }, { state: "error" }, { state: "active" }] });
      assert.equal(nodes.get("overview-downloads").textContent, "2");
      assert.equal(nodes.get("overview-download-errors").textContent, "1");
      assert.equal(nodes.get("overview-download-errors").attributes["data-attention"], "");
      receive(null, "offline");
      assert.equal(nodes.get("overview-download-errors").textContent, "—");
      assert.match(nodes.get("overview-download-errors-note").textContent, /不可用/);
    }
    subscriptions.get("forwards")({ items: [{ status: "error" }, { status: "retrying" }, { status: "done" }] });
    assert.equal(nodes.get("overview-forwards").textContent, "1");
    assert.equal(nodes.get("overview-forward-errors").textContent, "1");
    page.stop();
    assert.equal(subscriptions.size, 0);
  }
});

test("dashboard plots elapsed time and preserves missing readings in accessible sample details", async () => {
  const { nodes, subscriptions, samples, sample } = await mountDashboard();
  const receive = subscriptions.get("dashboard");
  nodes.get("dashboard-history").open = true;
  for (const offset of [0, 0, 5, 10, 15, 20, 25, 30, 35]) receive(sample(offset));
  assert.equal(samples.length, 7);
  assert.equal(samples[0].at, Date.parse(sample(5).sampled_at));
  receive(sample(10));
  assert.equal(samples.length, 7);
  receive(sample(35, { errors: { cpu: "unavailable", memory: "unavailable" } }));
  assert.equal(samples.length, 7);
  assert.equal(nodes.get("dashboard-cpu-value").textContent, "—");
  assert.equal(nodes.get("dashboard-memory-value").textContent, "—");
  assert.match(nodes.get("dashboard-status").textContent, /读取失败/);
  assert.match(nodes.get("dashboard-history-body").innerHTML, /<td>—<\/td><td>—<\/td><td>124 B<\/td>/);
  assert.doesNotMatch(nodes.get("dashboard-cpu-chart").innerHTML, /NaN/);
  assert.equal(nodes.get("dashboard-speed-value").textContent, "64 B/s");
  receive(null, "offline");
  assert.match(nodes.get("dashboard-status").textContent, /显示上次结果/);
  assert.match(nodes.get("dashboard-speed-chart").innerHTML, /最新采样/);
  assert.equal(nodes.get("dashboard-speed-value").textContent, "64 B/s");
});

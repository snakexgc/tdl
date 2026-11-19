import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

async function model() {
  const context = vm.createContext({ structuredClone });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/download.listen/assets/static/js/listen-model.js", import.meta.url), "utf8"), { context });
  await module.link(() => { throw new Error("unexpected model dependency"); });
  await module.evaluate();
  return module.namespace;
}

test("listen picker searches every supported peer kind and isolates drafts", async () => {
  const m = await model();
  const dialogs = [
    { ref: "channel:1", title: "频道", username: "videos", kind: "channel" },
    { ref: "channel:2", title: "群组", kind: "group" },
    { ref: "user:3", title: "机器人", username: "filesbot", kind: "bot" },
    { ref: "user:4", title: "用户", username: "alice", kind: "user" },
  ];
  assert.equal(m.matchingDialogs(dialogs, "@filesbot", "bot")[0].ref, "user:3");
  assert.equal(m.matchingDialogs(dialogs, "channel:2")[0].kind, "group");
  assert.equal(m.matchingDialogs(dialogs, "alice", "user")[0].ref, "user:4");
  assert.equal(m.hasChannel(["channel:2", "user:3"], dialogs), false);
  assert.equal(m.hasChannel(["channel:1", "user:4"], dialogs), true);
  const saved = [{ id: "one", name: "监听", enabled: true, sources: ["channel:1"], listen_comments: false }];
  const draft = m.cloneRules(saved);
  draft[0].sources.push("user:3");
  assert.equal(saved[0].sources.length, 1);
  assert.equal(m.rulesEqual(saved, draft), false);
  assert.equal(m.validateRules(saved), "");
  assert.match(m.validateRules([{ ...saved[0], sources: [] }]), /需要选择/);
});

test("listen tasks join live downloads by media ID and stay under their selected source", async () => {
  const m = await model();
  const jobs = [
    { id: "main", source: "chat:1", task_id: "media-1", message_id: 10, state: "accepted", file_name: "original.mp4" },
    { id: "comment", source: "channel:2", task_id: "media-2", message_id: 20, state: "accepted", comment: true },
    { id: "filter", source: "chat:1", task_id: "media-2", state: "filtered", file_name: "skip.mp4" },
    { id: "failed", source: "chat:1", task_id: "media-3", state: "pending", error: "retry pending" },
  ];
  const transfers = [
    { id: "gid-1", task_id: "media-1", state: "active", file_name: "renamed.mp4", total: 100, completed: 25, download_speed: 10, executor: "aria2" },
    { id: "local-2", task_id: "media-2", state: "complete", total: 200, completed: 200, executor: "local" },
    { id: "reaction-only", task_id: "foreign", state: "active" },
  ];
  const tasks = m.listenTasks(jobs, transfers);
  assert.equal(tasks.length, 4);
  assert.equal(tasks[0].state, "active");
  assert.equal(tasks[0].file_name, "renamed.mp4");
  assert.equal(tasks[0].completed, 25);
  assert.equal(tasks[0].download_speed, 10);
  assert.equal(tasks[1].state, "complete");
  assert.equal(tasks[2].state, "filtered", "filtered events do not inherit another download");
  assert.equal(tasks[3].error, "retry pending");
  const groups = m.listenSourceGroups([{ ref: "chat:1" }], tasks, [{ enabled: false, sources: ["channel:2"] }]);
  assert.equal(groups.length, 2);
  assert.equal(groups[0].tasks.length, 3);
  assert.equal(groups[1].tasks[0].comment, true);
  assert.equal(groups[1].enabled, false, "disabled sources retain all historical tasks");
});

test("listen status follows the runtime downloader and keeps admission history in link-only mode", async () => {
  class Element {
    constructor(tag, text = "") { this.tag = tag; this.textContent = text || ""; this.children = []; this.dataset = {}; }
    append(...children) { this.children.push(...children); }
    replaceChildren(...children) { this.children = children; }
    addEventListener() {}
    setAttribute() {}
    descendants() { return [this, ...this.children.flatMap(child => child.descendants())]; }
  }
  const nodes = new Map(), calls = [];
  const node = id => {
    if (!nodes.has(id)) nodes.set(id, new Element("div"));
    return nodes.get(id);
  };
  const media = { task_id: "media-1", source: "chat:1", message_id: 10, state: "accepted", file_name: "historical.mp4", total: 100 };
  let tick, transferFailure = false;
  const context = vm.createContext({
    document: { getElementById: node }, structuredClone, AbortSignal,
    setInterval: action => { tick = action; return 1; }, clearInterval() {},
  });
  const definitions = {
    "./api.js": { api: async url => {
      calls.push(url);
      if (url === "/api/components") return { components: [{ id: "download.listen", values: { rules: [] } }] };
      if (url === "/api/status") return { downloader: { mode: "local" } };
      if (url === "/api/dialogs") return { items: [] };
      if (url === "/api/listen-downloads/status") return { tasks: [media] };
      if (url.startsWith("/api/download-tasks?executor=")) {
        if (transferFailure) throw new Error("offline");
        return { items: [{ task_id: "media-1", state: "active", executor: url.split("=").pop(), total: 100, completed: 40 }] };
      }
      throw new Error("unexpected request " + url);
    } },
    "./account-profile.js": { loadCurrentAccount() {} },
    "./router.js": { navigate() {} },
    "./ui.js": { element: (tag, text) => new Element(tag, text), button() {}, openDrawer() {}, closeDrawer() {} },
    "./utils.js": { formatBytes: n => n + " B", formatTime: String },
  };
  const modules = {};
  for (const [name, directory] of [["listen-model", "download.listen"], ["download-model", "download.control"], ["state", "panel.webui"], ["status", "panel.webui"], ["listen-downloads", "download.listen"]]) {
    const path = new URL(`../../application/${directory}/assets/static/js/${name}.js`, import.meta.url);
    modules["./" + name + ".js"] = new vm.SourceTextModule(await readFile(path, "utf8"), { context });
  }
  const pageModule = modules["./listen-downloads.js"];
  await pageModule.link(name => modules[name] ||= new vm.SyntheticModule(Object.keys(definitions[name]), function () {
    for (const [key, value] of Object.entries(definitions[name])) this.setExport(key, value);
  }, { context }));
  await pageModule.evaluate();
  const page = pageModule.namespace.page;
  const sharedState = modules["./state.js"].namespace.state;
  const poll = async () => { tick(); await new Promise(resolve => setImmediate(resolve)); };
  const history = () => node("listen-sources").descendants();
  const taskCalls = () => calls.filter(url => url.startsWith("/api/download-tasks"));
  await page.load();
  assert.deepEqual(taskCalls(), ["/api/download-tasks?executor=local"]);
  assert(history().some(child => child.tag === "progress" && child.value === 40));
  transferFailure = true;
  await poll();
  assert.equal(node("listen-task-error").hidden, false);
  assert.match(node("listen-task-error").textContent, /上次取得/);
  assert(history().some(child => child.tag === "progress" && child.value === 40));
  for (const downloader of [{ mode: "http" }, { mode: "aria2", aria2_enabled: false }]) {
    sharedState.downloader = downloader;
    const previousCount = taskCalls().length;
    await poll();
    assert.equal(taskCalls().length, previousCount);
    assert(node("listen-task-error").hidden);
    assert(history().some(child => child.textContent === "historical.mp4"));
    assert(history().some(child => child.textContent === "已提交"));
    assert(!history().some(child => child.tag === "progress"));
  }
  sharedState.downloader = { mode: "aria2", aria2_enabled: true };
  transferFailure = false;
  await poll();
  assert.equal(taskCalls().at(-1), "/api/download-tasks?executor=aria2");
  assert(history().some(child => child.tag === "progress" && child.value === 40));
  assert.equal(calls.filter(url => url === "/api/status").length, 1);
  page.stop();
});

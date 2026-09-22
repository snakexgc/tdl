import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

class Element {
  constructor(tag = "div") {
    this.tagName = tag;
    this.children = [];
    this.listeners = new Map();
    this.attributes = new Map();
    this.disabled = false;
    this.hidden = false;
    this._textContent = "";
    this._innerHTML = "";
    this.links = [];
  }
  get textContent() { return this._textContent; }
  set textContent(value) { this._textContent = value; this._innerHTML = ""; this.links = []; }
  get innerHTML() { return this._innerHTML; }
  set innerHTML(value) {
    this._innerHTML = value;
    this._textContent = "";
    this.links = [...value.matchAll(/<a\s/g)].map(() => new Element("a"));
  }
  querySelectorAll(selector) { return selector === "a" ? this.links : []; }
  append(...children) { this.children.push(...children); }
  replaceChildren(...children) { this.children = children; }
  setAttribute(name, value) { this.attributes.set(name, value); }
  getAttribute(name) { return this.attributes.get(name); }
  removeAttribute(name) { this.attributes.delete(name); delete this[name]; }
  addEventListener(name, handler) { this.listeners.set(name, handler); }
  scrollIntoView() { this.scrolled = true; }
  fire(name = "click") { return this.listeners.get(name)?.(); }
}

function catalog(count = 5) {
  const channel = preview => Array.from({ length: count }, (_, i) => ({
    version: preview ? `v20260922_dev_abc${i}` : `v2026091${9 - i}1`,
    name: `${preview ? "Preview" : "Stable"} ${i}`,
    url: `https://github.com/test/repo/releases/tag/${preview ? "preview" : "stable"}-${i}`,
    release_notes: `${preview ? "preview" : "stable"} notes ${i}\n<img src=x onerror=alert(1)>`,
    published_at: "2026-09-22T01:00:00Z", prerelease: preview, current: false,
    can_install: true, asset_name: "tdl_Windows_64bit.zip", message: "可以切换到此版本",
  }));
  return { current_version: "v202609221", goos: "windows", goarch: "amd64", stable_releases: channel(false), preview_releases: channel(true) };
}

async function fixture({ update = catalog(), confirmed = true, check, apply } = {}) {
  const requests = [], prompts = [];
  const html = await readFile(new URL("../../application/update.self/assets/views/update.html", import.meta.url), "utf8");
  const elements = new Map([...html.matchAll(/id="([^"]+)"/g)].map(match => [match[1], new Element()]));
  const state = { update: null };
  const context = vm.createContext({
    AbortController, URL,
    document: { getElementById: id => elements.get(id), createElement: tag => new Element(tag) },
    confirm: message => { prompts.push(message); return confirmed; },
  });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/update.self/assets/static/js/update.js", import.meta.url), "utf8"), { context });
  const imports = {
    "./state.js": { state },
    "./utils.js": { infoItem: (label, value) => `${label}: ${value}` },
    "./api.js": { api: async (path, options = {}) => {
      requests.push({ path, options });
      if (path === "/api/update/check") return check ? check(options) : { update };
      assert.equal(path, "/api/update/apply");
      return apply ? apply(options) : { ok: true };
    } },
  };
  await module.link(path => new vm.SyntheticModule(Object.keys(imports[path]), function () {
    for (const [name, value] of Object.entries(imports[path])) this.setExport(name, value);
  }, { context }));
  await module.evaluate();
  module.namespace.page.init();
  return {
    requests, prompts, state, update,
    el: id => elements.get(id),
    load: module.namespace.page.load,
    stop: module.namespace.page.stop,
    select: (channel, index) => elements.get(`update-${channel}`).children[index].fire(),
    apply: () => elements.get("apply-update").fire(),
  };
}

test("update shows five releases per channel and safely falls back to plain text without rendered notes", async () => {
  const view = await fixture({ update: catalog(7) });
  await view.load();
  assert.equal(view.el("update-stable").children.length, 5);
  assert.equal(view.el("update-preview").children.length, 5);
  assert.equal(view.el("update-notes").textContent, view.update.stable_releases[0].release_notes);
  view.select("preview", 4);
  const release = view.update.preview_releases[4];
  assert.equal(view.el("update-notes").textContent, release.release_notes);
  assert.equal(view.el("update-notes").innerHTML, "");
  assert.equal(view.el("update-notes-shell").scrolled, true);
  assert.match(view.el("update-notes-title").textContent, new RegExp(release.version));
  assert.equal(view.el("update-release-link").href, release.url);
  assert.equal(view.el("update-stable").children[0].getAttribute("aria-pressed"), "false");
  assert.equal(view.el("update-preview").children[4].getAttribute("aria-pressed"), "true");
  assert.equal(view.el("apply-update").disabled, false);
  assert.equal(view.requests.length, 1, "reading notes must not download a release");
});

test("release selection displays server-rendered Markdown, opens links safely and resets scrolling", async () => {
  const update = catalog();
  const stable = update.stable_releases[0];
  const preview = update.preview_releases[2];
  stable.release_notes_html = '<h2>Stable</h2><ul><li><strong>Fixed</strong> downloads</li></ul><a href="https://github.com/test/repo">Details</a>';
  preview.release_notes_html = '<h2>Preview</h2><pre><code>tdl version</code></pre>';
  const view = await fixture({ update });
  await view.load();
  const notes = view.el("update-notes");
  assert.equal(notes.innerHTML, stable.release_notes_html);
  assert.equal(notes.querySelectorAll("a")[0].target, "_blank");
  assert.equal(notes.querySelectorAll("a")[0].rel, "noopener noreferrer");
  notes.scrollTop = 250;
  view.select("preview", 2);
  assert.equal(notes.innerHTML, preview.release_notes_html);
  assert.equal(notes.scrollTop, 0);
  view.select("stable", 1);
  assert.equal(notes.innerHTML, "", "plain-text fallback clears the previous rendered release");
  assert.equal(notes.textContent, update.stable_releases[1].release_notes);
  assert.equal(view.requests.length, 1, "selecting notes must not download a release");
});

test("confirmed downgrade or preview switch posts the exact selected tag", async () => {
  for (const channel of ["stable", "preview"]) {
    const view = await fixture();
    await view.load();
    view.select(channel, 3);
    const release = view.update[`${channel}_releases`][3];
    await view.apply();
    assert.match(view.prompts[0], new RegExp(release.version));
    assert.match(view.prompts[0], /v202609221/);
    assert.equal(view.requests[1].options.method, "POST");
    assert.deepEqual(JSON.parse(view.requests[1].options.body), { version: release.version });
    assert.equal(view.el("apply-update").disabled, true);
    assert.equal(view.el("check-update").disabled, true);
    assert.equal(view.el("update-notes").textContent, release.release_notes);
    await view.load();
    await view.apply();
    assert.equal(view.requests.length, 2, "do not recheck or reinstall during restart");
  }
});

test("cancelling version switch sends no apply request", async () => {
  const view = await fixture({ confirmed: false });
  await view.load();
  view.select("preview", 1);
  await view.apply();
  assert.equal(view.requests.length, 1);
  assert.equal(view.el("apply-update").disabled, false);
});

test("current, incompatible and container releases keep notes accessible without allowing install", async () => {
  for (const kind of ["current", "incompatible", "docker"]) {
    const update = catalog();
    const release = update.preview_releases[2];
    if (kind === "current") release.current = true;
    if (kind === "incompatible") release.can_install = false;
    if (kind === "docker") update.docker = true;
    release.message = "此版本不可切换";
    const view = await fixture({ update });
    await view.load();
    view.select("preview", 2);
    assert.equal(view.el("update-notes").textContent, release.release_notes);
    assert.match(view.el("update-selection-status").textContent, /此版本不可切换/);
    assert.equal(view.el("apply-update").disabled, true);
    await view.apply();
    assert.equal(view.requests.length, 1);
    assert.equal(view.prompts.length, 0);
  }
});

test("download pins the selection, rejects repeated clicks and allows retry after failure", async () => {
  let reject;
  let calls = 0;
  const pending = new Promise((resolve, failure) => { reject = failure; });
  const view = await fixture({ apply: () => ++calls === 1 ? pending : { ok: true } });
  await view.load();
  view.select("preview", 2);
  const download = view.apply();
  assert.equal(view.el("apply-update").disabled, true);
  assert.equal(view.el("update-preview").children[0].disabled, true);
  view.select("stable", 0);
  await view.apply();
  await view.load();
  assert.equal(view.requests.length, 2);
  reject(new Error("download unavailable"));
  await download;
  assert.match(view.el("update-status").textContent, /download unavailable/);
  assert.equal(view.el("apply-update").disabled, false);
  await view.apply();
  assert.equal(view.requests.length, 3);
  assert.equal(JSON.parse(view.requests[2].options.body).version, view.update.preview_releases[2].version);
});

test("failed recheck clears stale install targets", async () => {
  let calls = 0;
  const view = await fixture({ check: () => {
    if (++calls === 1) return { update: catalog() };
    throw new Error("GitHub unavailable");
  } });
  await view.load();
  view.select("preview", 1);
  await view.load();
  assert.equal(view.el("apply-update").disabled, true);
  assert.equal(view.state.update, null);
  assert.match(view.el("update-status").textContent, /GitHub unavailable/);
  await view.apply();
  assert.equal(view.requests.length, 2);
});

test("superseded or abandoned checks cannot overwrite the current catalog", async () => {
  const pending = [];
  const view = await fixture({ check: options => new Promise(resolve => pending.push({ resolve, signal: options.signal })) });
  const first = view.load();
  const second = view.load();
  assert.equal(pending[0].signal.aborted, true);
  const newest = catalog(1);
  pending[1].resolve({ update: newest });
  await second;
  pending[0].resolve({ update: catalog(5) });
  await first;
  assert.equal(view.state.update, newest);
  assert.equal(view.el("update-stable").children.length, 1);
  const third = view.load();
  view.stop();
  assert.equal(pending[2].signal.aborted, true);
  pending[2].resolve({ update: catalog() });
  await third;
  assert.equal(view.state.update, null);
});

test("empty channels and missing notes have clear placeholders and unsafe links are hidden", async () => {
  const update = catalog(1);
  update.stable_releases = [];
  update.preview_releases[0].release_notes = "";
  update.preview_releases[0].url = "javascript:alert(1)";
  const view = await fixture({ update });
  await view.load();
  assert.match(view.el("update-stable").children[0].textContent, /暂无已发布/);
  assert.match(view.el("update-notes").textContent, /未提供 Release Notes/);
  assert.equal(view.el("update-release-link").hidden, true);
  assert.equal(view.el("update-release-link").href, undefined);
});

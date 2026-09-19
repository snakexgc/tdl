import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("module tabs preserve drafts, scope settings, and stop inactive observations", async () => {
  class Element {
    constructor(tag = "div") { this.tag = tag; this.children = []; this.events = new Map(); this.attributes = {}; this.hidden = false; }
    get childNodes() { return this.children; }
    append(...nodes) { for (const node of nodes) { if (node.parent) node.parent.children = node.parent.children.filter(child => child !== node); node.parent = this; this.children.push(node); } }
    prepend(node) { this.append(node); this.children = [node, ...this.children.filter(child => child !== node)]; }
    replaceChildren(...nodes) { this.children = []; this.append(...nodes); }
    setAttribute(name, value) { this.attributes[name] = value; }
    addEventListener(name, callback) { this.events.set(name, callback); }
    querySelector(selector) { return selector === ".page-head" ? this.children.find(node => node.className === "page-head") : null; }
    querySelectorAll() { return []; }
    focus() { this.focused = true; }
    async click() { await this.events.get("click")?.(); }
  }
  let renders = 0, fetches = 0, loads = 0, stops = 0, active = true, scope;
  const context = vm.createContext({ document: { createElement: tag => new Element(tag) } });
  const modules = {
    "./api.js": new vm.SyntheticModule(["api"], function() { this.setExport("api", async () => { fetches++; return { editable: true, components: [] }; }); }, { context }),
    "./component-form.js": new vm.SyntheticModule(["renderComponentForms"], function() { this.setExport("renderComponentForms", async (host, _, data, ids) => { renders++; scope = ids; host.draft = "initial"; }); }, { context }),
  };
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/settings-tabs.js", import.meta.url), "utf8"), { context });
  await module.link(name => modules[name]); await module.evaluate();
  const root = new Element(), header = new Element("header"), content = new Element();
  header.className = "page-head"; root.append(header, content);
  const page = { section: root, view: "downloads", title: "Downloads", settings: ["download.control"], hooks: { load: async () => loads++, stop: () => stops++ } };
  const tabs = module.namespace.mountSettingsTabs(page, () => active);
  const [, bar, info, settings] = root.children;
  assert.equal(info.children[0], content);
  await bar.children[1].click();
  assert.equal(stops, 1); assert.equal(fetches, 1); assert.deepEqual(scope, page.settings);
  assert(tabs.settingsActive()); assert(info.hidden);
  const forms = settings.children[1]; forms.draft = "unsaved changes";
  await bar.children[0].click(); await bar.children[1].click();
  assert.equal(loads, 1); assert.equal(renders, 1); assert.equal(forms.draft, "unsaved changes");
  await settings.children[0].click(); await new Promise(setImmediate);
  assert.equal(renders, 2);
  active = false;
  await bar.children[0].click();
  assert.equal(loads, 1, "late actions must not revive an inactive view");
});

import assert from "node:assert/strict";
import { webcrypto } from "node:crypto";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("forward rules can be added and copied over HTTP, and searched by @username", async () => {
  class Element {
    constructor(tag) { this.tag = tag; this.children = []; this.events = new Map(); this.value = ""; }
    append(...children) { this.children.push(...children); if (this.tag === "select" && !this.value) this.value = children[0].value; }
    replaceChildren(...children) { this.children = children; }
    setAttribute() {}
    addEventListener(name, callback) { this.events.set(name, callback); }
    querySelector() { return null; }
    dispatchEvent() {}
    get lastElementChild() { return this.children.at(-1); }
    click() { this.events.get("click")?.(); }
  }
  const context = vm.createContext({
    // Simulate a browser HTTP context with no crypto.randomUUID.
    crypto: { getRandomValues: bytes => webcrypto.getRandomValues(bytes) },
    Uint8Array, structuredClone, Event,
    document: { querySelector: () => ({}), createElement: tag => new Element(tag) },
  });
  const api = new vm.SyntheticModule(["api"], function() {
    this.setExport("api", async () => ({ items: [{ ref: "channel:123", title: "Project", username: "projectteam", kind: "group" }] }));
  }, { context });
  let drawer, close;
  const ui = new vm.SyntheticModule(["openDrawer", "closeDrawer"], function() {
    this.setExport("openDrawer", (_title, content, onClose) => { drawer = content; close = onClose; });
    this.setExport("closeDrawer", () => { close?.(); drawer = null; });
  }, { context });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/forward.rules/assets/static/js/forward-rules.js", import.meta.url), "utf8"), { context });
  await module.link(specifier => specifier === "./ui.js" ? ui : api); await module.evaluate();
  const editor = await module.namespace.createEditor([], { editable: true });
  await new Promise(setImmediate);
  editor.element.children[2].click();
  const cards = editor.element.children[4];
  assert.equal(editor.value().length, 1);
  const source = drawer.children[1];
  const search = source.children[1], results = source.children[4];
  search.value = "@projectteam"; search.events.get("input")();
  assert.equal(results.children[0].tag, "label", "@username search finds the chat");
  const checkbox = results.children[0].children[0];
  checkbox.checked = true; checkbox.events.get("change")();
  close();
  cards.children[0].children[1].children[4].click();
  const rules = editor.value();
  assert.equal(rules.length, 2);
  assert.notEqual(rules[0].id, rules[1].id);
  assert.deepEqual(rules[0].sources, ["channel:123"]);
  rules[0].sources.push("channel:456");
  assert.deepEqual(editor.value()[0].sources, ["channel:123"], "returned values cannot mutate the editor");
  assert.deepEqual(rules[1].sources, ["channel:123"], "copy has independent selections");
});

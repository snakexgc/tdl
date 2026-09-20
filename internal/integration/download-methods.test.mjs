import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const descendants = (node) => [node, ...node.children.flatMap(descendants)];
class Element {
  constructor(tag) {
    this.tag = tag;
    this.children = [];
    this.dataset = {};
    this.attributes = {};
    this.events = new Map();
    this.classList = { toggle() {} };
  }
  append(...children) {
    this.children.push(...children);
  }
  replaceChildren(...children) {
    this.children = children;
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
  }
  setCustomValidity(value) {
    this.validation = value;
  }
  addEventListener(name, action) {
    this.events.set(name, action);
  }
  dispatchEvent(event) {
    this.events.get(event.type)?.(event);
  }
  querySelector(selector) {
    return descendants(this).find(
      (node) => node.dataset.method === selector.match(/"(.*?)"/)[1],
    );
  }
  focus() {}
}

async function load() {
  const context = vm.createContext({ Event });
  const element = (tag, text) =>
    Object.assign(new Element(tag), { textContent: text });
  const ui = new vm.SyntheticModule(
    ["element", "button"],
    function () {
      this.setExport("element", element);
      this.setExport("button", (text, action) => {
        const node = element("button", text);
        node.addEventListener("click", action);
        return node;
      });
    },
    { context },
  );
  const module = new vm.SourceTextModule(
    await readFile(
      new URL(
        "../../application/download.control/assets/static/js/download-methods.js",
        import.meta.url,
      ),
      "utf8",
    ),
    { context },
  );
  await module.link(() => ui);
  await module.evaluate();
  return module.namespace;
}

test("download method controls preserve order, pin HTTP last and validate an empty selection", async () => {
  const { createEditor } = await load();
  const original = ["aria2", "http"],
    editor = await createEditor(original, { editable: true });
  const value = () => Array.from(editor.value());
  const toggle = (method, checked) => {
    const input = descendants(editor.element).find(
      (node) => node.dataset.method === method,
    );
    input.checked = checked;
    input.dispatchEvent(new Event("change"));
  };
  const action = (name) =>
    descendants(editor.element).find(
      (node) => node.attributes["aria-label"] === name,
    );
  let changes = 0;
  editor.element.addEventListener("change", () => changes++);
  toggle("local", true);
  assert.deepEqual(value(), ["aria2", "local", "http"]);
  assert.equal(action("下移保存到本机").disabled, true);
  action("上移保存到本机").dispatchEvent(new Event("click"));
  assert.deepEqual(value(), ["local", "aria2", "http"]);
  assert.equal(action("上移保存到本机").disabled, true);
  assert.equal(changes, 2);
  assert.deepEqual(original, ["aria2", "http"]);
  editor.value().push("unexpected");
  assert.equal(value().length, 3);
  for (const method of value()) toggle(method, false);
  assert.deepEqual(value(), []);
  assert(descendants(editor.element).some((node) => node.validation));
  toggle("http", true);
  toggle("local", true);
  assert.deepEqual(value(), ["local", "http"]);
  assert(!descendants(editor.element).some((node) => node.validation));
});

test("download methods remain visible but cannot be changed in readonly mode", async () => {
  const { createEditor } = await load();
  const editor = await createEditor(["http"], { editable: false });
  for (const control of descendants(editor.element).filter((node) =>
    ["input", "button"].includes(node.tag),
  ))
    assert.equal(control.disabled, true);
  assert.deepEqual(Array.from(editor.value()), ["http"]);
});

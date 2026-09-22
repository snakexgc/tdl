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

test("download method controls select one downloader and keep HTTP links available", async () => {
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
  let changes = 0;
  editor.element.addEventListener("change", () => changes++);
  toggle("local", true);
  assert.deepEqual(value(), ["local", "http"]);
  const controls = descendants(editor.element).filter((node) => node.tag === "input");
  assert.equal(controls.length, 2);
  assert(controls.every((input) => input.type === "radio"));
  assert.equal(controls.filter((input) => input.checked).length, 1);
  assert.equal(changes, 1);
  assert.deepEqual(original, ["aria2", "http"]);
  editor.value().push("unexpected");
  assert.equal(value().length, 2);
  toggle("aria2", true);
  assert.deepEqual(value(), ["aria2", "http"]);
});

test("legacy multiple downloaders keep only the first choice", async () => {
  const { createEditor } = await load();
  const editor = await createEditor(["local", "aria2", "http"], { editable: true });
  assert.deepEqual(Array.from(editor.value()), ["local", "http"]);
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

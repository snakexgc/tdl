import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("mobile navigation isolates content, cycles focus, closes and resets at desktop width", async () => {
  const handlers = new Map();
  const classes = new Set();
  let activeElement;
  function element(name) {
    return {
      attributes: {},
      addEventListener: (event, callback) => handlers.set(`${name}:${event}`, callback),
      setAttribute(key, value) { this.attributes[key] = value; },
      focus() { activeElement = this; },
    };
  }
  const sidebar = element("sidebar"), toggle = element("toggle");
  const backdrop = element("backdrop"), content = element("content");
  const close = element("close");
  const link = element("first"), current = element("current"), logout = element("logout");
  const header = element("header"), skip = element("skip"), warning = element("warning");
  let linksReady = true;
  sidebar.querySelector = selector => linksReady ? (selector === ".nav-item.active" ? current : link) : null;
  sidebar.querySelectorAll = () => linksReady ? [close, link, current, logout] : [close, logout];
  sidebar.contains = node => sidebar.querySelectorAll().includes(node);
  const mobile = { matches: true, addEventListener: (_, callback) => handlers.set("resize", callback) };
  const context = vm.createContext({
    document: {
      body: { classList: { toggle: (name, on) => on ? classes.add(name) : classes.delete(name) } },
      getElementById: id => ({ "app-sidebar": sidebar, "menu-toggle": toggle, "sidebar-backdrop": backdrop, "sidebar-close": close })[id],
      querySelector: selector => selector === ".skip-link" ? skip : content,
      querySelectorAll: () => [skip, header, content, warning],
      addEventListener: (name, callback) => handlers.set(`document:${name}`, callback),
      get activeElement() { return activeElement; },
    },
    window: {
      matchMedia: () => mobile,
      addEventListener: (name, callback) => handlers.set(`window:${name}`, callback),
    },
  });
  const source = await readFile(new URL("../../application/panel.webui/assets/static/js/shell.js", import.meta.url), "utf8");
  const module = new vm.SourceTextModule(source, { context });
  await module.link(() => {});
  await module.evaluate();
  module.namespace.initShell();

  assert.equal(sidebar.inert, true);
  assert.equal(content.inert, false);
  assert.equal(backdrop.hidden, true);
  handlers.get("toggle:click")();
  assert.equal(toggle.attributes["aria-expanded"], "true");
  assert.equal(sidebar.inert, false);
  assert.equal(content.inert, true);
  assert.equal(backdrop.hidden, false);
  assert.equal(activeElement, current);
  for (const background of [skip, header, warning]) assert.equal(background.inert, true);
  assert(classes.has("navigation-open"));

  let prevented = false;
  const key = (value, shiftKey = false) => handlers.get("document:keydown")({
    key: value, shiftKey, preventDefault() { prevented = true; },
  });
  close.focus();
  key("Tab", true);
  assert.equal(activeElement, logout);
  assert.equal(prevented, true);
  key("Tab");
  assert.equal(activeElement, close);
  key("Escape");
  assert.equal(activeElement, toggle);
  assert.equal(toggle.attributes["aria-expanded"], "false");
  assert.equal(content.inert, false);
  for (const background of [skip, header, warning]) assert.equal(background.inert, false);
  assert.equal(sidebar.inert, true);
  assert(!classes.has("navigation-open"));

  handlers.get("toggle:click")();
  handlers.get("window:page-selected")();
  assert.equal(sidebar.inert, true);
  assert.equal(activeElement, content);
  handlers.get("toggle:click")();
  handlers.get("backdrop:click")();
  assert.equal(backdrop.hidden, true);
  assert.equal(content.inert, false);
  handlers.get("toggle:click")();
  handlers.get("close:click")();
  assert.equal(sidebar.inert, true);
  assert.equal(activeElement, toggle);

  linksReady = false;
  handlers.get("toggle:click")();
  assert.equal(activeElement, close);
  linksReady = true;
  handlers.get("close:click")();
  handlers.get("toggle:click")();
  mobile.matches = false;
  handlers.get("resize")();
  assert.equal(sidebar.inert, false);
  assert.equal(content.inert, false);
  assert.equal(toggle.attributes["aria-expanded"], "false");
  assert.equal(backdrop.hidden, true);
  handlers.get("toggle:click")();
  assert(!classes.has("navigation-open"));
  current.focus();
  mobile.matches = true;
  handlers.get("resize")();
  assert.equal(activeElement, toggle);
  let skipPrevented = false;
  handlers.get("skip:click")({ preventDefault() { skipPrevented = true; } });
  assert(skipPrevented);
  assert.equal(activeElement, content);
});

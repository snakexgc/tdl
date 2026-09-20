import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import path from "node:path";
import test from "node:test";
import vm from "node:vm";

// Exercise the generic loader without a browser, a build step or third-party DOM.
test("declared pages mount lazily, redirect aliases, preserve query and guide disabled features", async () => {
  class Element {
    constructor() {
      this.children = [];
      this.dataset = {};
      this.classes = new Set();
      this.classList = {
        toggle: (name, on) => on ? this.classes.add(name) : this.classes.delete(name),
        contains: name => this.classes.has(name),
      };
    }
    append(child) { this.children.push(child); }
    replaceChildren() { this.children = []; }
    querySelectorAll() { return []; }
    addEventListener() {}
  }
  const host = new Element(), nav = new Element(), head = new Element();
  const find = (node, id) => node.id === id ? node : node.children.map(child => find(child, id)).find(Boolean);
  const calls = [], fetched = [];
  const components = [
    { id: "example", enabled: true, pages: [
      { path: "/example", title: "Example", view: "example", module: "/static/js/example.js" },
      { path: "/other", title: "Other", view: "other", module: "/static/js/other.js" },
      { path: "/logs", title: "Logs", view: "logs", module: "/static/js/logs.js" },
      { path: "/old", title: "Old", nav_hidden: true, redirect_to: "/other?tab=links#details" },
    ] },
    { id: "disabled", enabled: false, pages: [{ path: "/disabled", title: "Disabled", view: "disabled" }] },
    { id: "sleeping", enabled: false, pages: [{ path: "/sleeping", title: "Sleeping", view: "sleeping", keep_visible: true, settings_url: "/example?tab=settings" }] },
  ];
  const context = vm.createContext({
    URL, Intl,
    window: {
      location: { origin: "http://panel.test", pathname: "/example" },
      history: { pushState() {} }, addEventListener() {},
    },
    document: {
      head,
      createElement: () => new Element(),
      getElementById: id => id === "view-host" ? host : find(host, id),
      querySelector: selector => selector === "nav.nav" ? nav : undefined,
    },
    fetch: async url => {
      fetched.push(url);
      return { ok: true, status: 200, text: async () => url === "/api/components" ? JSON.stringify({ components }) : "<section>Example</section>" };
    },
  });
  const root = fileURLToPath(new URL("../../application/panel.webui/assets/static/js/", import.meta.url));
  const modules = new Map();
  async function load(name) {
    if (modules.has(name)) return modules.get(name);
    const module = new vm.SourceTextModule(await readFile(path.join(root, name), "utf8"), {
      context, identifier: name,
      importModuleDynamically: async specifier => {
        const feature = new vm.SyntheticModule(["page"], function () {
          this.setExport("page", {
            init: () => calls.push([specifier, "init"]),
            load: url => calls.push([specifier, "load", url?.search, url?.hash]),
            stop: () => calls.push([specifier, "stop"]),
          });
        }, { context });
        await feature.link(() => {});
        await feature.evaluate();
        return feature;
      },
    });
    modules.set(name, module);
    await module.link(specifier => load(specifier.replace(/^\.\//, "")));
    return module;
  }
  const router = await load("router.js");
  await router.evaluate();
  await router.namespace.initRouter();
  assert.equal(nav.children.length, 4);
  assert.deepEqual(fetched, ["/api/components", "/views/example.html"]);
  await router.namespace.navigate("other");
  assert(calls.some(([module, action]) => module.endsWith("example.js") && action === "stop"));
  assert(calls.some(([module, action]) => module.endsWith("other.js") && action === "load"));
  await router.namespace.navigate("example");
  assert.equal(calls.filter(([module, action]) => module.endsWith("example.js") && action === "init").length, 1);
  await router.namespace.navigate("disabled");
  assert.equal(find(host, "page-unavailable").hidden, false);
  assert(!fetched.includes("/views/disabled.html"));
  await router.namespace.navigate("/old");
  assert(calls.some(([module, action, query, hash]) => module.endsWith("other.js") && action === "load" && query === "?tab=links" && hash === "#details"));
  assert(!fetched.includes("/views/old.html"));
  await router.namespace.navigate("/sleeping");
  assert.match(find(host, "page-sleeping").innerHTML, /相关服务已停用/);
  assert(!fetched.includes("/views/sleeping.html"));
  for (const alias of ["/modules#diagnostics", "/config?tab=system#diagnostics"]) {
    await router.namespace.navigate(alias);
    assert.deepEqual(calls.at(-1), ["/static/js/logs.js", "load", "?tab=health", ""]);
  }
  await router.namespace.navigate("/logs?component=downloader.local&level=warn");
  assert.equal(calls.at(-1)[2], "?component=downloader.local&level=warn");
  router.namespace.stopPages();
});

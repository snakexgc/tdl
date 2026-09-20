import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("account identity coalesces heartbeats and a newer account refresh wins over an old response", async () => {
  const elements = new Map(["current-account", "account-name", "account-detail", "account-avatar"].map(id => [id, { textContent: "", setAttribute(name, value) { this[name] = value; } }]));
  let calls = 0, resolve, onRefresh;
  const context = vm.createContext({
    AbortSignal,
    document: { getElementById: id => elements.get(id) },
    window: { addEventListener: (_name, fn) => { onRefresh = fn; } },
  });
  const api = new vm.SyntheticModule(["api"], function () {
    this.setExport("api", () => { calls++; return new Promise(done => { resolve = done; }); });
  }, { context });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/account-profile.js", import.meta.url), "utf8"), { context });
  await module.link(() => api); await module.evaluate();
  const pending = module.namespace.loadCurrentAccount("account-a");
  await module.namespace.loadCurrentAccount("account-a");
  assert.equal(calls, 1);
  onRefresh({ detail: { valid: true, user: { name: "Alice Example", username: "alice" } } });
  resolve({ valid: true, user: { name: "Old name" } });
  await pending;
  assert.equal(elements.get("account-name").textContent, "Alice Example");
  assert.equal(elements.get("account-detail").textContent, "@alice");
  assert.equal(elements.get("account-avatar").textContent, "AE");
  await module.namespace.loadCurrentAccount("account-a");
  assert.equal(calls, 1);
  onRefresh({ detail: { valid: false, namespace: "not-a-telegram-name" } });
  assert.equal(elements.get("account-name").textContent, "未登录账号");
  assert.match(elements.get("account-detail").textContent, /登录/);
});

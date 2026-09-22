import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

async function fixture({ confirmed = true, clean, status } = {}) {
  const requests = [], prompts = [], notices = [];
  let refreshed = 0;
  const context = vm.createContext({ confirm: message => { prompts.push(message); return confirmed; } });
  const module = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/storage-maintenance.js", import.meta.url), "utf8"), { context });
  await module.link(() => new vm.SyntheticModule(["api"], function () {
    this.setExport("api", async (path, options) => {
      requests.push({ path, options });
      if (path === "/api/status") return status ? status() : { namespace: "Alice" };
      return clean ? clean() : { ok: true, namespace: "Alice", deleted: 3, kept: 7 };
    });
  }, { context }));
  await module.evaluate();
  const button = { disabled: false, isConnected: true, textContent: "清理存储" };
  return {
    button, requests, prompts, notices,
    get refreshed() { return refreshed; },
    run: () => module.namespace.cleanStorage(button, {
      refresh: async () => { refreshed++; },
      notify: (...args) => notices.push(args),
    }),
  };
}

test("cancelling storage cleanup never submits a destructive request", async () => {
  const view = await fixture({ confirmed: false });
  await view.run();
  assert.deepEqual(view.requests.map(request => request.path), ["/api/status"]);
  assert.equal(view.refreshed, 0);
  assert.equal(view.button.disabled, false);
  assert.match(view.prompts[0], /Alice/);
  assert.match(view.prompts[0], /不可撤销/);
  assert.match(view.prompts[0], /不删除已下载文件/);
});

test("confirmation binds cleanup to the displayed account and refreshes the result", async () => {
  const view = await fixture();
  await view.run();
  assert.equal(view.requests[1].path, "/api/storage/clean");
  assert.equal(view.requests[1].options.method, "POST");
  assert.deepEqual(JSON.parse(view.requests[1].options.body), { confirmation: "CLEAN_STORAGE", namespace: "Alice" });
  assert.equal(view.refreshed, 1);
  assert.match(view.notices.at(-1)[0], /已删除 3 条，保留 7 条/);
  assert.equal(view.notices.at(-1)[1], "success");
  assert.equal(view.button.textContent, "清理存储");
  assert.equal(view.button.disabled, false);
});

test("cleanup rejects duplicate clicks while the request is pending", async () => {
  let complete;
  const pending = new Promise(resolve => { complete = resolve; });
  const view = await fixture({ clean: () => pending });
  const first = view.run();
  await view.run();
  assert.equal(view.button.disabled, true);
  complete({ ok: true, namespace: "Alice", deleted: 0, kept: 0 });
  await first;
  assert.equal(view.requests.filter(request => request.path === "/api/storage/clean").length, 1);
});

test("cleanup reports partial failures and restores the button after request errors", async () => {
  const partial = await fixture({ clean: () => ({ ok: false, namespace: "Alice", deleted: 2, kept: 7, errors: ["write failed"] }) });
  await partial.run();
  assert.equal(partial.refreshed, 1);
  assert.match(partial.notices.at(-1)[0], /未全部完成.*已删除 2 条.*write failed/);
  assert.equal(partial.notices.at(-1)[1], "error");
  const failed = await fixture({ clean: () => { throw new Error("unavailable"); } });
  await failed.run();
  assert.match(failed.notices.at(-1)[0], /unavailable/);
  assert.equal(failed.button.disabled, false);
  assert.equal(failed.button.textContent, "清理存储");
});

test("leaving the page or failing to identify the account prevents cleanup", async () => {
  const missing = await fixture({ status: () => ({}) });
  await missing.run();
  assert.equal(missing.prompts.length, 0);
  assert.equal(missing.requests.length, 1);
  assert.equal(missing.notices.at(-1)[1], "error");
  const detached = await fixture();
  detached.button.isConnected = false;
  await detached.run();
  assert.equal(detached.prompts.length, 0);
  assert.equal(detached.requests.length, 1);
});

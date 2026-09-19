import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

test("one websocket multiplexes views and HTTP fallback stops after recovery", async () => {
  const sockets = [], intervals = new Map(), timers = new Map(), hooks = new Map(), requests = [];
  const indicator = { dataset: {} }, label = {};
  let nextTimer = 0;
  class Socket {
    static OPEN = 1;
    constructor(url) { this.url = url; this.readyState = 0; this.sent = []; sockets.push(this); }
    send(value) { this.sent.push(JSON.parse(value)); }
    close() { this.readyState = 3; this.onclose?.({ code: 1000 }); }
    open() { this.readyState = 1; this.onopen(); }
  }
  const context = vm.createContext({
    URL, WebSocket: Socket, AbortSignal, Math, console,
    window: { location: { href: "http://panel.test/dashboard" }, addEventListener: (name, callback) => hooks.set(name, callback) },
    document: { hidden: false, getElementById: id => id.endsWith("label") ? label : indicator },
    setTimeout: callback => { timers.set(++nextTimer, callback); return nextTimer; }, clearTimeout: id => timers.delete(id),
    setInterval: callback => { intervals.set(++nextTimer, callback); return nextTimer; }, clearInterval: id => intervals.delete(id),
    fetch: async path => { requests.push(path); return { ok: true, json: async () => ({ mode: "local" }) }; },
  });
  const mod = new vm.SourceTextModule(await readFile(new URL("../../application/panel.webui/assets/static/js/events.js", import.meta.url), "utf8"), { context });
  await mod.link(() => {}); await mod.evaluate();
  const received = [];
  mod.namespace.observe("status", data => received.push(data));
  const leave = mod.namespace.observe("downloads", () => {});
  mod.namespace.startRealtime(); mod.namespace.startRealtime();
  assert.equal(sockets.length, 1);
  sockets[0].open();
  assert.deepEqual(Array.from(sockets[0].sent.at(-1).topics), ["status", "downloads"]);
  sockets[0].onmessage({ data: JSON.stringify({ topic: "status", data: { mode: "aria2" } }) });
  assert.equal(received[0].mode, "aria2");
  leave();
  assert.deepEqual(Array.from(sockets[0].sent.at(-1).topics), ["status"]);
  sockets[0].close();
  await new Promise(setImmediate);
  assert.deepEqual(requests, ["/api/status"]);
  assert.equal(received.at(-1).mode, "local");
  assert.equal(indicator.dataset.state, "fallback");
  timers.values().next().value();
  sockets[1].open();
  for (const callback of intervals.values()) callback();
  await new Promise(setImmediate);
  assert.equal(requests.length, 1);
  const count = received.length;
  sockets[0].onmessage({ data: JSON.stringify({ topic: "status", data: { mode: "stale" } }) });
  assert.equal(received.length, count);
  mod.namespace.stopRealtime();
  assert.equal(intervals.size, 0);
  assert.equal(sockets[1].readyState, 3);
});

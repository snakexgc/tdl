import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

async function load(relative) {
  const module = new vm.SourceTextModule(
    await readFile(new URL(relative, import.meta.url), "utf8"),
    { context: vm.createContext({ structuredClone }) },
  );
  await module.link(() => {
    throw new Error("Model should not require DOM or network");
  });
  await module.evaluate();
  return module.namespace;
}
const models = await load(
  "../../application/panel.webui/assets/static/js/settings-model.js",
);
test("settings search locates advanced fields and system settings without indexing values", () => {
  const items = [
    {
      id: "downloader.aria2",
      values: { secret: "private-value", directory: "private-directory" },
      fields: [
        {
          name: "directory",
          title: "保存目录",
          settings_tab: "download",
          settings_section: "aria2 连接",
          help: "下载到 aria2 所在机器",
        },
        {
          name: "secret",
          title: "RPC 密钥",
          secret: true,
          default: "private-default",
          settings_tab: "download",
        },
        {
          name: "connect_retry_ms",
          title: "首次重试间隔",
          advanced: true,
          settings_tab: "download",
          settings_section: "aria2 连接",
        },
      ],
    },
  ];
  assert.equal(
    models.searchSettings(items, "aria2 目录")[0].target,
    "setting-downloader.aria2-directory",
  );
  assert.equal(
    models.searchSettings(items, "CONNECT_RETRY_MS")[0].target,
    "setting-downloader.aria2-connect_retry_ms",
  );
  assert.equal(models.searchSettings(items, "namespace")[0].tab, "system");
  assert.equal(
    models.searchSettings(items, "debug")[0].target,
    "setting-system-debug",
  );
  for (const query of [
    "private-value",
    "private-directory",
    "private-default",
    "   ",
  ])
    assert.equal(models.searchSettings(items, query).length, 0);
});

test("empty required numbers remain drafts after reloading a zero baseline", () => {
  const item = {
    id: "filter.rules",
    fields: [{ name: "min_mb", type: "int", default: 0 }],
    values: { min_mb: 0 },
  };
  const store = new models.ConfigurationDrafts([item]);
  store.set(item.id, "min_mb", "");
  store.rebase([item]);
  assert.equal(store.count, 1);
  assert.equal(store.value(item.id, "min_mb"), "");
  store.set(item.id, "min_mb", 0);
  assert.equal(store.dirty, false);
});

test("saved addresses fill inputs without replacing hidden credentials on unchanged saves", () => {
  const item = {
    id: "connection",
    fields: [
      { name: "proxy", secret: true, format: "proxy" },
      { name: "rpc_url", secret: true, format: "url" },
      { name: "password", secret: true },
      { name: "timeout", type: "int", default: 30 },
    ],
    previews: {
      proxy: "socks5://127.0.0.1:1080",
      rpc_url: "https://example.test/jsonrpc",
    },
  };
  const store = new models.ConfigurationDrafts([item]);
  assert.equal(store.value(item.id, "proxy"), item.previews.proxy);
  assert.equal(store.value(item.id, "rpc_url"), item.previews.rpc_url);
  assert.equal(store.value(item.id, "password"), "");
  assert.equal(store.dirty, false);
  // Editing and reverting an address must not submit its redacted value.
  for (const name of ["proxy", "rpc_url"]) {
    store.set(item.id, name, "https://changed.test:1080");
    assert.equal(store.count, 1);
    store.set(item.id, name, item.previews[name]);
    assert.equal(store.dirty, false);
    store.set(item.id, name, "");
    assert.equal(store.dirty, false);
  }
  store.set(item.id, "timeout", 60);
  assert.equal(
    JSON.stringify(
      store.patch(
        item.id,
        item.fields.map((f) => f.name),
      ),
    ),
    '{"timeout":60}',
  );
  store.set(item.id, "proxy", "socks5://new-user:new-password@127.0.0.1:1080");
  assert.equal(
    store.patch(item.id, ["proxy"]).proxy,
    "socks5://new-user:new-password@127.0.0.1:1080",
  );
  store.accept(
    { ...item, previews: { ...item.previews, proxy: "socks5://[::1]:1080" } },
    ["proxy"],
  );
  assert.equal(store.value(item.id, "proxy"), "socks5://[::1]:1080");
});

test("settings hints keep secrets private and distinguish zero, false and empty lists", () => {
  assert.equal(models.displayValue({ secret: true }, "secret"), "不回显");
  assert.doesNotMatch(
    models.fieldHint({ secret: true, default: "private-default" }),
    /private-default/,
  );
  assert.match(
    models.fieldHint({ type: "int", default: 0, min: 0, max: 100 }),
    /默认：0/,
  );
  assert.equal(models.displayValue({ type: "bool" }, false), "关闭");
  assert.equal(models.displayValue({ type: "strings" }, []), "空列表");
});

test("pending changes combine saved component and system differences with readable values", () => {
  const components = [
    {
      id: "example",
      title: "示例",
      fields: [
        {
          name: "limit",
          title: "并发数量",
          type: "int",
          settings_tab: "download",
          settings_section: "下载",
        },
      ],
      changes: [
        { name: "limit", before: 1, after: 3 },
        { name: "enabled", before: true, after: false },
        {
          name: "password",
          secret: true,
          before: "已设置（不回显）",
          after: "已设置（不回显）",
        },
      ],
    },
  ];
  const system = {
    active_config: { namespace: "default", debug: false },
    config: { namespace: "default", debug: true },
  };
  const changes = models.pendingSettings(components, system);
  assert.equal(changes.length, 4);
  assert.equal(changes[0].title, "并发数量");
  assert.equal(changes[0].tab, "download");
  assert.equal(models.changeValue(changes[0], "before"), "1");
  assert.equal(models.changeValue(changes[0], "after"), "3");
  assert.equal(models.changeValue(changes[1], "after"), "关闭");
  assert.equal(
    models.changeValue(changes[2], "after"),
    "已设置（不回显） · 已修改",
  );
  assert.equal(changes[3].title, "详细日志");
  assert.equal(models.changeValue(changes[3], "after"), "开启");
  components[0].changes = [];
  system.config.debug = false;
  assert.equal(models.pendingSettings(components, system).length, 0);
});

test("rule changes preserve nested values for review", () => {
  const rules = [{ name: "收藏", targets: ["self"], enabled: false }];
  assert.equal(
    models.changeValue({ after: rules }, "after"),
    JSON.stringify(rules, null, 2),
  );
  assert.match(
    models.fieldHint({ type: "int", restart_required: false }),
    /重启/,
  );
});

test("the change summary merges drafts with saved differences and never renders secret input", () => {
  const item = {
    id: "bot",
    title: "机器人",
    fields: [
      {
        name: "allowed_users",
        title: "允许操作的用户",
        type: "strings",
        default: [],
        settings_tab: "bot",
      },
      {
        name: "token",
        title: "Token",
        secret: true,
        default: "",
        settings_tab: "bot",
      },
    ],
    values: { allowed_users: ["123"] },
    changes: [{ name: "allowed_users", before: [], after: ["123"] }],
  };
  const drafts = new models.ConfigurationDrafts([item]);
  drafts.set("bot", "allowed_users", ["456"]);
  drafts.set("bot", "token", "private-token");
  const system = { active_config: { debug: false }, config: { debug: true } };
  const changes = models.pendingSettings([item], system, drafts, false);
  assert.equal(changes.length, 3);
  const users = changes.find((change) => change.name === "allowed_users");
  assert.equal(users.draft, true);
  assert.equal(JSON.stringify(users.before), "[]");
  assert.equal(JSON.stringify(users.after), '["456"]');
  assert.doesNotMatch(JSON.stringify(changes), /private-token/);
  const debug = changes.find((change) => change.name === "debug");
  assert.equal(debug.draft, true);
  assert.equal(debug.before, false);
  assert.equal(debug.after, false);
  assert.equal(
    models.pendingSettings([], {}, new models.ConfigurationDrafts([])).length,
    0,
  );
});

test("settings validate related fields and allow unlimited file size", () => {
  const check = (id, values) =>
    models.settingsErrors(id, (name) => values[name]);
  assert.equal(
    check("filter.rules", { include: ["mp4"], exclude: ["jpg"] }).size,
    2,
  );
  assert.equal(check("filter.rules", { min_mb: 100, max_mb: 0 }).size, 0);
  assert.equal(
    check("filter.rules", { min_mb: 100, max_mb: 99 }).has("max_mb"),
    true,
  );
  assert.equal(
    check("download.control", {
      executors: ["local", "http"],
      local_root: "",
    }).has("local_root"),
    false,
  );
  assert.equal(
    check("download.control", {
      executors: ["http", "local"],
      local_root: "/downloads",
    }).has("executors"),
    true,
  );
  assert.equal(
    check("download.control", { executors: ["local", "local"] }).has(
      "executors",
    ),
    true,
  );
  assert.equal(
    check("download.control", { executors: ["aria2", "http"] }).size,
    0,
  );
  assert.equal(
    check("downloader.aria2", {
      connect_retry_ms: 1000,
      connect_retry_max_ms: 100,
    }).has("connect_retry_max_ms"),
    true,
  );
  assert.equal(
    check("forwarder", {
      retry_base_seconds: 1000,
      retry_max_seconds: 100,
    }).has("retry_max_seconds"),
    true,
  );
});

const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// One revision and draft per component, even when fields span multiple tabs.
export class ConfigurationDrafts {
  constructor(components) {
    this.components = new Map();
    this.drafts = new Map();
    this.rebase(components);
  }
  field(id, name) {
    return this.components
      .get(id)
      ?.fields?.find((field) => field.name === name);
  }
  baseline(id, name) {
    const field = this.field(id, name);
    return field?.secret
      ? (this.components.get(id)?.previews?.[name] ?? "")
      : (this.components.get(id)?.values?.[name] ?? field?.default);
  }
  value(id, name) {
    return this.drafts.get(id)?.has(name)
      ? this.drafts.get(id).get(name)
      : this.baseline(id, name);
  }
  set(id, name, value) {
    if (!this.drafts.has(id)) this.drafts.set(id, new Map());
    const fields = this.drafts.get(id);
    if (
      equal(value, this.baseline(id, name)) ||
      ((this.field(id, name)?.secret ||
        this.field(id, name)?.empty_preserves) &&
        value === "")
    )
      fields.delete(name);
    else fields.set(name, structuredClone(value));
    if (!fields.size) this.drafts.delete(id);
  }
  patch(id, names) {
    return Object.fromEntries(
      names
        .filter((name) => this.drafts.get(id)?.has(name))
        .map((name) => [name, this.drafts.get(id).get(name)]),
    );
  }
  reset(id, names) {
    for (const name of names) this.drafts.get(id)?.delete(name);
    if (!this.drafts.get(id)?.size) this.drafts.delete(id);
  }
  accept(component, names) {
    this.components.set(component.id, structuredClone(component));
    this.reset(component.id, names);
  }
  rebase(components) {
    this.components = new Map(
      components.map((component) => [component.id, structuredClone(component)]),
    );
    for (const [id, fields] of this.drafts)
      for (const [name, value] of fields) {
        if (!this.field(id, name)) this.reset(id, [name]);
        else this.set(id, name, value);
      }
  }
  get dirty() {
    return this.drafts.size > 0;
  }
  get count() {
    return [...this.drafts.values()].reduce(
      (sum, fields) => sum + fields.size,
      0,
    );
  }
}

export const settingsTabs = [
  [
    "network",
    "网络连接",
    "设置 Telegram、机器人和软件更新共用的代理，以及时间校准和断线重试间隔。",
  ],
  [
    "download",
    "下载",
    "先选择下载方式和保存位置，再调整并发、文件过滤与命名。以下目录均指服务所在机器上的路径。",
  ],
  [
    "forward",
    "转发",
    "用规则指定从哪里转发到哪里，再设置自动监听范围。启用或停用自动转发请前往模块管理。",
  ],
  [
    "account",
    "账号",
    "通常使用内置 API 凭据即可。登录、退出和切换 Telegram 账号请前往账号管理。",
  ],
  [
    "links",
    "下载链接",
    "让 aria2 或其他下载工具通过链接读取 Telegram 文件。请填写下载者实际能够访问的地址。",
  ],
  [
    "bot",
    "机器人",
    "连接 Telegram 机器人，并指定哪些用户可以执行机器人命令。网络代理在「网络连接」中统一设置。",
  ],
  [
    "notifications",
    "通知",
    "先配置机器人和接收者，再选择需要通知的下载事件。通知接收者与机器人命令的允许用户分别设置。",
  ],
  [
    "panel",
    "面板访问",
    "管理此 WebUI 的登录凭据和访问地址。更改凭据后需重新登录，更改监听地址或端口后需重新访问。",
  ],
  [
    "system",
    "系统",
    "查看当前账号数据空间、调整日志，以及重启或重置应用。所有业务配置由各账号共用。",
  ],
];
export function fieldTab(field) {
  return settingsTabs.some(([id]) => id === field.settings_tab)
    ? field.settings_tab
    : "system";
}
export function settingsGroups(components, tab) {
  const groups = [];
  for (const component of components) {
    const sections = new Map();
    for (const field of component.fields || []) {
      if (fieldTab(field) !== tab) continue;
      const title = field.settings_section || "组件配置";
      if (!sections.has(title)) sections.set(title, []);
      sections.get(title).push(field);
    }
    for (const [title, fields] of sections)
      groups.push({ id: component.id, title, fields });
  }
  // The rule editor is the primary entry in forwarding settings.
  return groups.sort(
    (a, b) =>
      Number(!a.fields.some((f) => f.editor)) -
        Number(!b.fields.some((f) => f.editor)) ||
      Math.min(...a.fields.map((f) => f.settings_order ?? 100)) -
        Math.min(...b.fields.map((f) => f.settings_order ?? 100)),
  );
}

export function settingID(id, name) {
  return `setting-${id}-${name}`;
}

export function displayValue(field, value) {
  if (field.secret) return "不回显";
  if (field.type === "bool") return value ? "开启" : "关闭";
  if (Array.isArray(value))
    return field.type === "objects"
      ? `${value.length} 条规则`
      : value.length
        ? value.join("、")
        : "空列表";
  if (value === "" || value == null) return "留空";
  return field.choice_labels?.[value] ?? String(value);
}

export function fieldHint(field) {
  const parts = [];
  if (field.secret && ["proxy", "url"].includes(field.format))
    parts.push("认证信息不回显，地址未修改时保留原值");
  else if (field.secret) parts.push("不回显原值，留空保留");
  else parts.push(`默认：${displayValue(field, field.default)}`);
  if (field.type === "int") {
    if (field.min != null && field.max != null)
      parts.push(`范围：${field.min}–${field.max}`);
    else if (field.min != null) parts.push(`最小：${field.min}`);
    else if (field.max != null) parts.push(`最大：${field.max}`);
  }
  parts.push("保存后需重启生效");
  return parts.join(" · ");
}

export function pendingSettings(components, system, drafts, debugDraft) {
  const result = [];
  for (const component of components) {
    for (const change of component.changes || []) {
      const field = component.fields?.find((item) => item.name === change.name);
      result.push({
        ...change,
        id: component.id,
        title:
          field?.title ||
          (change.name === "enabled" ? "功能开关" : change.name),
        section: field?.settings_section || component.title || component.id,
        tab: field ? fieldTab(field) : "system",
        field,
      });
    }
  }
  if (system?.active_config && system.config) {
    for (const [name, title] of [
      ["debug", "详细日志"],
      ["namespace", "账号数据空间"],
    ]) {
      if (system.active_config[name] !== system.config[name])
        result.push({
          id: "system",
          name,
          title,
          section: "系统",
          tab: "system",
          before: system.active_config[name],
          after: system.config[name],
          field: { type: name === "debug" ? "bool" : "string" },
        });
    }
  }
  for (const [id, values] of drafts?.drafts || []) {
    for (const [name, value] of values) {
      const field = drafts.field(id, name),
        saved = result.find(
          (change) => change.id === id && change.name === name,
        ),
        change = saved || {
          id,
          name,
          field,
          title: field.title || name,
          section: field.settings_section || drafts.components.get(id).title,
          tab: fieldTab(field),
          secret: field.secret,
          before: field.secret ? "不回显" : drafts.baseline(id, name),
        };
      change.after = field.secret ? "不回显" : value;
      change.draft = true;
      if (!saved) result.push(change);
    }
  }
  if (debugDraft !== undefined) {
    const saved = result.find(
        (change) => change.id === "system" && change.name === "debug",
      ),
      change = saved || {
        id: "system",
        name: "debug",
        title: "详细日志",
        section: "系统",
        tab: "system",
        field: { type: "bool" },
        before: system.active_config?.debug ?? system.config?.debug,
      };
    change.after = debugDraft;
    change.draft = true;
    if (!saved) result.push(change);
  }
  return result;
}

export function changeValue(change, side) {
  const value = change[side];
  if (change.secret) return `${value}${side === "after" ? " · 已修改" : ""}`;
  if (value && typeof value === "object") return JSON.stringify(value, null, 2);
  return displayValue(change.field || { type: "bool" }, value);
}

// Cross-field checks provide immediate guidance; the server remains authoritative.
export function settingsErrors(id, value) {
  const errors = new Map();
  if (id === "filter.rules") {
    if (value("include")?.length && value("exclude")?.length) {
      errors.set("include", "允许列表和排除列表只能填写一个，请清空其中一个。");
      errors.set("exclude", errors.get("include"));
    }
    if (
      Number(value("max_mb")) > 0 &&
      Number(value("min_mb")) > Number(value("max_mb"))
    )
      errors.set(
        "max_mb",
        "最大文件大小不能小于最小文件大小；填 0 表示不限制。",
      );
  }
  const retry = {
    "downloader.aria2": ["connect_retry_ms", "connect_retry_max_ms"],
    forwarder: ["retry_base_seconds", "retry_max_seconds"],
  }[id];
  if (retry && Number(value(retry[0])) > Number(value(retry[1])))
    errors.set(retry[1], "最大重试间隔不能小于首次重试间隔。");
  if (id === "download.control") {
    const executors = value("executors") || [];
    if (
      !executors.length ||
      executors.some((item) => !["local", "aria2", "http"].includes(item))
    )
      errors.set(
        "executors",
        "每行填写 local、aria2 或 http，至少选择一种下载方式。",
      );
    else if (new Set(executors).size !== executors.length)
      errors.set("executors", "下载方式不能重复。");
    else if (executors.includes("http") && executors.at(-1) !== "http")
      errors.set("executors", "http 只生成链接，需要放在最后一行。");
  }
  return errors;
}

// Search schema text only. Saved values and secret drafts never enter the index.
export function searchSettings(components, query) {
  const words = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return [];
  const entries = settingsTabs.flatMap(([tab, title]) =>
    settingsGroups(components, tab).flatMap((group) =>
      group.fields.map((field) => ({
        tab,
        category: title,
        section: group.title,
        title: field.title || field.name,
        help: field.help || "",
        key: `${group.id}.${field.name}`,
        target: settingID(group.id, field.name),
      })),
    ),
  );
  entries.push(
    {
      tab: "system",
      category: "系统",
      section: "日志",
      title: "详细日志",
      help: "排查问题 debug",
      key: "system.debug",
      target: "setting-system-debug",
    },
    {
      tab: "system",
      category: "系统",
      section: "当前账号",
      title: "账号数据空间",
      help: "namespace 登录会话与历史任务，在账号管理中切换",
      key: "system.namespace",
      target: "setting-system-namespace",
    },
  );
  return entries.filter((entry) => {
    const text = [
      entry.category,
      entry.section,
      entry.title,
      entry.help,
      entry.key,
    ]
      .join(" ")
      .toLocaleLowerCase();
    return words.every((word) => text.includes(word));
  });
}

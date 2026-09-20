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
      ? ""
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
      (this.field(id, name)?.secret && value === "")
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
  ["network", "网络配置"],
  ["download", "下载设置"],
  ["forward", "转发设置"],
  ["account", "账号设置"],
  ["links", "链接设置"],
  ["bot", "机器人设置"],
  ["notifications", "通知设置"],
  ["panel", "面板设置"],
  ["system", "系统设置"],
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
      if (field.replaced_by || fieldTab(field) !== tab) continue;
      const title = field.settings_section || "高级组件配置";
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
      Math.min(...a.fields.map((f) => f.settings_order || 100)) -
        Math.min(...b.fields.map((f) => f.settings_order || 100)),
  );
}

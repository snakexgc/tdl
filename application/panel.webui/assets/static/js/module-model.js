export function featureGroups(components) {
  const groups = new Map();
  for (const component of components) {
    const feature = component.feature?.id
      ? component.feature
      : { id: "extensions", title: "扩展功能", order: 1000 };
    if (!groups.has(feature.id))
      groups.set(feature.id, { ...feature, components: [] });
    groups.get(feature.id).components.push(component);
  }
  return [...groups.values()].sort(
    (a, b) => (a.order || 100) - (b.order || 100),
  );
}

export function componentStatus(component) {
  if (component.error) return { text: "异常", kind: "error" };
  if (component.enabled === false) return { text: "已停用", kind: "off" };
  if (component.pending_restart) return { text: "等待重启", kind: "waiting" };
  const states = {
    running: ["运行中", "running"],
    starting: ["启动中", "waiting"],
    stopping: ["停止中", "waiting"],
    failed: ["异常", "error"],
    blocked: ["依赖未就绪", "waiting"],
    stopped: ["未运行", "waiting"],
  };
  const [text, kind] = states[component.state] || ["状态未知", "waiting"];
  return { text, kind };
}

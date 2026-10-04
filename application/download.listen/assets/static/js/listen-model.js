export const sourceKinds = { group: "群组", channel: "频道", user: "用户", bot: "机器人" };
export const cloneRules = value => structuredClone(Array.isArray(value) ? value : []);
export const rulesEqual = (a, b) => JSON.stringify(a) === JSON.stringify(b);
export const sourceName = (ref, dialogs) => dialogs.find(item => item.ref === ref)?.title || ref;

export function matchingDialogs(dialogs, query, kind = "all") {
  const term = query.trim().toLocaleLowerCase();
  return dialogs.filter(item => (kind === "all" || item.kind === kind) &&
    `${item.title || ""} @${item.username || ""} ${item.ref}`.toLocaleLowerCase().includes(term));
}

export function hasChannel(sources, dialogs) {
  return sources.some(ref => {
    const item = dialogs.find(item => item.ref === ref);
    return item ? item.kind === "channel" : ref.startsWith("channel:");
  });
}

export function validateRules(rules) {
  if (rules.length > 200) return "最多支持 200 条监听规则。";
  const ids = new Set();
  for (const rule of rules) {
    if (!rule.id || ids.has(rule.id)) return "规则 ID 为空或重复，请重新添加该规则。";
    ids.add(rule.id);
    if ([...rule.name].length > 100) return "规则名称最多 100 个字符。";
    if (!rule.sources.length || rule.sources.length > 500) return `“${rule.name || "未命名规则"}”需要选择 1–500 个来源。`;
  }
  return "";
}

// Transfer state comes from the same account download-control API as /downloads.
// Admission failures/filtered events never inherit an unrelated transfer state.
export function listenTasks(jobs, downloads) {
  const byMedia = new Map();
  for (const task of downloads) {
    const key = task.task_id || task.id;
    const previous = byMedia.get(key);
    if (!previous || new Date(task.created_at || 0) > new Date(previous.created_at || 0)) byMedia.set(key, task);
  }
  return jobs.map(job => {
    const transfer = ["accepted", "submitting", "uncertain"].includes(job.state) ? byMedia.get(job.task_id) : null;
    return {
      ...job,
      state: transfer ? transfer.state || transfer.status || "unknown" : job.state,
      file_name: transfer?.file_name || job.file_name || job.task_id || `消息 #${job.message_id}`,
      total: Number(transfer?.total || job.total || 0),
      completed: Number(transfer?.completed || 0),
      download_speed: Number(transfer?.download_speed || 0),
      error: transfer ? transfer.error || "" : job.error || "",
      executor: transfer?.executor || "",
    };
  });
}

export function listenSourceGroups(sources, jobs, savedRules) {
  const groups = new Map(sources.map(source => [source.ref, { ...source, enabled: true, tasks: [] }]));
  for (const ref of savedRules.flatMap(rule => rule.sources || [])) {
    if (!groups.has(ref)) groups.set(ref, { ref, enabled: false, tasks: [] });
  }
  for (const job of jobs) {
    if (!job.source) continue;
    if (!groups.has(job.source)) groups.set(job.source, { ref: job.source, enabled: false, tasks: [] });
    groups.get(job.source).tasks.push(job);
  }
  return [...groups.values()];
}

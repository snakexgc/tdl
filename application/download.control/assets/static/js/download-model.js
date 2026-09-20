export const executors = ["local", "aria2"];
export const executorLabel = (value) => (value === "local" ? "本地" : "aria2");
export const taskKey = (task) =>
  JSON.stringify([task.account, task.executor, task.id]);
export const taskState = (task) =>
  task.state ||
  { waiting: "queued", downloading: "active", done: "complete" }[task.status] ||
  task.status ||
  "unknown";
export const statusLabel = (value) =>
  ({
    queued: "排队中",
    waiting: "排队中",
    active: "下载中",
    paused: "已暂停",
    complete: "已完成",
    error: "出错",
    removed: "已移除",
    unknown: "未知",
  })[value] ||
  value ||
  "—";
export function eligible(task, action) {
  const value = taskState(task);
  if (action === "delete") return true;
  if (action === "pause")
    return (
      ["active", "queued"].includes(value) ||
      (task.executor === "local" && value === "error")
    );
  return value === "paused" || (task.executor === "local" && value === "error");
}
export function groupActions(tasks, action) {
  const groups = new Map();
  for (const task of tasks.filter((task) => eligible(task, action))) {
    const key = JSON.stringify([task.account, task.executor]);
    if (!groups.has(key))
      groups.set(key, {
        account: task.account,
        executor: task.executor,
        action,
        ids: [],
      });
    const ids = groups.get(key).ids;
    if (!ids.includes(task.id)) ids.push(task.id);
  }
  return [...groups.values()];
}

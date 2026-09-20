import { api } from "./api.js";
import { observe } from "./events.js";
import { navigate } from "./router.js";
import {
  escapeHTML as esc,
  escapeAttr,
  formatBytes,
  formatTime,
} from "./utils.js";
import {
  fragment,
  element,
  openDrawer,
  closeDrawer,
  tabKeyboard,
  activateTabs,
} from "./ui.js";
import {
  executors,
  executorLabel,
  taskKey,
  taskState,
  statusLabel,
  statusClass,
  groupActions,
  eligible,
} from "./download-model.js";

let snapshots = {},
  errors = {},
  selected = new Set(),
  releases = [],
  active = false,
  generation = 0,
  linksReady,
  linksPage,
  busy = false;
const $ = (id) => document.getElementById(id);
const allTasks = () =>
  executors.flatMap((executor) => snapshots[executor] || []);
const visible = () =>
  allTasks().filter(
    (task) =>
      ($("download-executor").value === "all" ||
        task.executor === $("download-executor").value) &&
      ($("download-state").value === "all" ||
        taskState(task) === $("download-state").value) &&
      (task.file_name || task.id)
        .toLocaleLowerCase()
        .includes($("download-search").value.trim().toLocaleLowerCase()),
  );
const time = (value) =>
  value && !String(value).startsWith("0001-") ? formatTime(value) : "—";

function init() {
  snapshots = {};
  errors = {};
  selected = new Set();
  releases = [];
  linksReady = null;
  linksPage = null;
  tabKeyboard($("download-tabs"));
  $("download-tabs")
    .querySelectorAll("[data-tab]")
    .forEach((tab) =>
      tab.addEventListener("click", () =>
        navigate(`/downloads?tab=${tab.dataset.tab}`),
      ),
    );
  for (const id of ["download-search", "download-executor", "download-state"])
    $(id).addEventListener(
      id === "download-search" ? "input" : "change",
      () => {
        selected.clear();
        render();
      },
    );
  $("download-refresh").addEventListener("click", refresh);
  $("download-select").addEventListener("change", (event) => {
    const choice = event.target.value;
    selected.clear();
    if (choice !== "clear")
      for (const task of visible())
        if (
          choice === "all" ||
          (choice === "unfinished"
            ? !["complete", "removed"].includes(taskState(task))
            : taskState(task) === choice)
        )
          selected.add(taskKey(task));
    event.target.value = "";
    render();
  });
  $("download-check-all").addEventListener("change", (event) => {
    selected = new Set(event.target.checked ? visible().map(taskKey) : []);
    render();
  });
  $("download-body").addEventListener("change", (event) => {
    const check = event.target.closest("[data-task-check]");
    if (!check) return;
    if (check.checked) selected.add(check.dataset.taskCheck);
    else selected.delete(check.dataset.taskCheck);
    selection();
  });
  $("download-body").addEventListener("click", (event) => {
    const action = event.target.closest("[data-task-action]");
    if (!action) return;
    const task = allTasks().find(
      (task) => taskKey(task) === action.dataset.taskKey,
    );
    if (!task) return;
    if (action.dataset.taskAction === "details") details(task);
    else void control(action.dataset.taskAction, [task]);
  });
  $("download-bulk")
    .querySelectorAll("[data-download-bulk]")
    .forEach((button) =>
      button.addEventListener("click", () =>
        control(
          button.dataset.downloadBulk,
          allTasks().filter((task) => selected.has(taskKey(task))),
        ),
      ),
    );
  const protocol = location.protocol.replace(":", "");
  $("advanced-download").href =
    `/aria2ng.html#!/settings/rpc/set?${new URLSearchParams({ protocol, host: location.hostname, port: location.port || (protocol === "https" ? "443" : "80"), interface: "aria2/jsonrpc" })}`;
}
async function load(url) {
  const tab = url?.searchParams?.get("tab") === "links" ? "links" : "tasks";
  activateTabs($("download-tabs"), tab);
  $("download-tasks-panel").hidden = tab !== "tasks";
  $("download-links-panel").hidden = tab !== "links";
  if (tab === "links") {
    stop();
    linksReady ||= (async () => {
      await fragment($("download-links-panel"), "kv");
      linksPage = (await import("./kv.js")).page;
      linksPage.init();
    })().catch((error) => {
      linksReady = null;
      throw error;
    });
    await linksReady;
    if (
      location.pathname === "/downloads" &&
      new URLSearchParams(location.search).get("tab") === "links"
    )
      await linksPage.load();
    return;
  }
  linksPage?.stop?.();
  active = true;
  if (!releases.length)
    for (const executor of executors)
      releases.push(
        observe(`download-tasks-${executor}`, (data, error) =>
          receive(executor, data, error),
        ),
      );
  $("download-state").value = url?.searchParams?.get("state") || "all";
  render();
  await refresh();
}
export async function loadDownloads() {
  if (active) await refresh();
}
async function refresh() {
  if (!active) return;
  const version = ++generation;
  await Promise.all(
    executors.map(async (executor) => {
      try {
        const data = await api(`/api/download-tasks?executor=${executor}`, {
          signal: AbortSignal.timeout(5000),
        });
        if (version === generation && active) receive(executor, data);
      } catch (error) {
        if (version === generation && active)
          receive(executor, null, error.message);
      }
    }),
  );
}
function receive(executor, data, error) {
  if (!active) return;
  if (error) errors[executor] = error;
  else {
    snapshots[executor] = (data?.items || []).map((task) => ({
      ...task,
      executor,
    }));
    delete errors[executor];
  }
  const valid = new Set(allTasks().map(taskKey));
  selected = new Set([...selected].filter((key) => valid.has(key)));
  render();
}
function render() {
  const focus = document.activeElement?.closest?.(
    "#download-body [data-task-key], #download-body [data-task-check]",
  );
  const focusKey = focus?.dataset.taskKey || focus?.dataset.taskCheck,
    focusAction = focus?.dataset.taskAction;
  $("download-health").innerHTML = executors
    .map(
      (executor) =>
        `<span class="${errors[executor] ? "bad-text" : "subtle"}">${executorLabel(executor)}：${errors[executor] ? `连接不可用${snapshots[executor] ? "，保留上次数据" : ""} · ${esc(errors[executor])}` : snapshots[executor] ? `${snapshots[executor].length} 项任务` : "连接中…"}</span>`,
    )
    .join("");
  const tasks = visible().sort(
    (a, b) => (Date.parse(b.created_at) || 0) - (Date.parse(a.created_at) || 0),
  );
  $("download-body").innerHTML = tasks.length
    ? tasks.map(row).join("")
    : '<tr><td colspan="8" class="empty">没有符合条件的任务。<p>通过 Telegram 触发下载，或在「链接记录」中提交下载。</p></td></tr>';
  if (focusKey)
    [
      ...$("download-body").querySelectorAll(
        "[data-task-key], [data-task-check]",
      ),
    ]
      .find(
        (node) =>
          (node.dataset.taskKey || node.dataset.taskCheck) === focusKey &&
          node.dataset.taskAction === focusAction,
      )
      ?.focus({ preventScroll: true });
  selection();
}
function row(task) {
  const key = escapeAttr(taskKey(task)),
    value = taskState(task),
    percent =
      task.total > 0
        ? Math.min(100, Math.max(0, (task.completed / task.total) * 100))
        : 0;
  const action =
    value === "error"
      ? eligible(task, "resume")
        ? "resume"
        : ""
      : eligible(task, "pause")
        ? "pause"
        : eligible(task, "resume")
          ? "resume"
          : "";
  const controls = action
    ? `<button class="btn secondary compact" data-task-key="${key}" data-task-action="${action}" ${busy || errors[task.executor] ? "disabled" : ""}>${action === "pause" ? "暂停" : "恢复"}</button>`
    : "";
  return `<tr><td><input type="checkbox" data-task-check="${key}" aria-label="选择 ${escapeAttr(task.file_name || task.id)}" ${selected.has(taskKey(task)) ? "checked" : ""}></td>
    <td class="task-name"><button class="text-button" data-task-key="${key}" data-task-action="details">${esc(task.file_name || task.id)}</button>${errors[task.executor] ? '<small class="bad-text">数据已过期</small>' : ""}</td>
    <td>${executorLabel(task.executor)}</td><td><span class="pill ${statusClass(value)}">${esc(statusLabel(value))}</span></td>
    <td class="task-progress"><div>${task.total > 0 ? `${formatBytes(task.completed || 0)} / ${formatBytes(task.total)}` : "—"}</div><progress max="100" value="${percent}" aria-label="${escapeAttr(statusLabel(value))}进度"></progress></td>
    <td class="time-cell">${task.download_speed != null ? `${formatBytes(task.download_speed)}/s` : "—"}</td><td class="time-cell">${time(task.created_at)}</td>
    <td><div class="row-actions">${controls}<button class="btn secondary compact" data-task-key="${key}" data-task-action="details">详情</button><button class="btn danger compact" data-task-key="${key}" data-task-action="delete" ${busy || errors[task.executor] ? "disabled" : ""}>删除</button></div></td></tr>`;
}
function selection() {
  const tasks = visible(),
    count = selected.size;
  $("download-bulk").hidden = count === 0;
  $("download-selection-count").textContent = `已选 ${count} 项`;
  $("download-check-all").checked =
    tasks.length > 0 && tasks.every((task) => selected.has(taskKey(task)));
  $("download-check-all").indeterminate =
    count > 0 && !$("download-check-all").checked;
  $("download-bulk")
    .querySelectorAll("button")
    .forEach((button) => {
      button.disabled =
        busy ||
        !allTasks().some(
          (task) =>
            selected.has(taskKey(task)) &&
            !errors[task.executor] &&
            eligible(task, button.dataset.downloadBulk),
        );
    });
}
function details(task) {
  const content = element("dl", null, "detail-list");
  for (const [label, value] of [
    ["文件", task.file_name],
    ["执行器", executorLabel(task.executor)],
    ["状态", statusLabel(taskState(task))],
    ["账号", task.account],
    ["任务 ID", task.id],
    ["来源记录", task.task_id],
    ["保存路径", task.path || task.dir],
    ["创建时间", time(task.created_at)],
    ["开始时间", time(task.started_at)],
    ["更新时间", time(task.updated_at)],
    [
      "已用时间",
      task.elapsed_seconds > 0 ? `${task.elapsed_seconds} 秒` : null,
    ],
    ["预计剩余", task.eta_seconds > 0 ? `${task.eta_seconds} 秒` : null],
    ["错误", task.error],
  ])
    content.append(element("dt", label), element("dd", value || "—"));
  if (task.executor === "aria2" && taskState(task) === "error")
    content.append(
      element("dt", "重新下载"),
      element("dd", "此 aria2 任务已终止，可在「链接记录」中重新提交下载。"),
    );
  openDrawer("下载任务详情", content);
}
async function control(action, tasks) {
  if (busy) return;
  if (
    action === "delete" &&
    !confirm(
      `删除选中的 ${tasks.length} 项下载任务？本地未完成任务的临时文件会清理，已完成文件保留；aria2 移除任务或结果记录，不主动删除下载文件。`,
    )
  )
    return;
  const available = tasks.filter((task) => !errors[task.executor]),
    groups = groupActions(available, action);
  if (!groups.length) return;
  busy = true;
  render();
  let changed = 0,
    skipped =
      tasks.length - available.filter((task) => eligible(task, action)).length;
  const failures = [];
  await Promise.all(
    groups.map(async (request) => {
      try {
        const data = await api("/api/download-tasks/actions", {
          method: "POST",
          body: JSON.stringify(request),
        });
        changed += data.result?.changed || 0;
        skipped += data.result?.skipped || 0;
        failures.push(
          ...(data.result?.errors || []).map(
            (error) => `${executorLabel(request.executor)}：${error}`,
          ),
        );
      } catch (error) {
        failures.push(`${executorLabel(request.executor)}：${error.message}`);
      }
    }),
  );
  busy = false;
  if (!failures.length) selected.clear();
  await refresh();
  $("download-message").className =
    `notice ${failures.length ? "error" : "success"}`;
  $("download-message").textContent =
    `已处理 ${changed} 项，跳过 ${skipped} 项${failures.length ? `；${failures.join("；")}` : ""}`;
  selection();
}
function stop() {
  active = false;
  ++generation;
  releases.forEach((release) => release());
  releases = [];
  linksPage?.stop?.();
  closeDrawer();
}
export const page = { init, load, stop };

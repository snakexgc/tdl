import { api } from "./api.js";
import { element, button, openDrawer, closeDrawer } from "./ui.js";
import { formatTime, formatBytes } from "./utils.js";
import { statusLabel, statusClass } from "./download-model.js";
import { cloneRules, rulesEqual, matchingDialogs, hasChannel, sourceName, sourceKinds, validateRules, listenTasks, listenSourceGroups } from "./listen-model.js";

const ID = "download.listen";
let component, rules = [], dialogs = [], editable = false, busy = false, active = false,
  generation = 0, timer, catalogError = "", statusError = "", dialogRequest, statusRequest,
  lastStatus, downloadTasks = [], downloadError = "";
const $ = id => document.getElementById(id);
const dirty = () => !rulesEqual(rules, component?.values?.rules || []);

function report(message, success = false) {
  const host = $("listen-error");
  if (!host) return;
  host.hidden = !message;
  host.className = `notice ${success ? "success" : "error"}`;
  host.textContent = message;
}

function updateDraft() {
  const changed = dirty();
  $("listen-draft-state").textContent = changed ? "尚未保存的修改 · 保存后立即生效" : "修改规则后点击保存";
  $("listen-save").disabled = !editable || busy || !changed;
  $("listen-save").textContent = busy ? "正在保存…" : "保存";
  $("listen-discard").disabled = busy || !changed;
  $("listen-add").disabled = busy || !editable;
}

function renderRules() {
  const host = $("listen-rule-list");
  host.replaceChildren();
  if (!rules.length) {
    const empty = element("div", null, "empty listen-empty");
    empty.append(element("h3", "还没有监听规则"), element("p", "添加规则，选择群组、频道、机器人或用户。首次启用只处理之后的新消息。"));
    host.append(empty);
  }
  rules.forEach((rule, index) => {
    const card = element("article", null, "listen-rule-card");
    const head = element("div", null, "listen-rule-head"), title = element("h3", rule.name || "未命名规则");
    const enabled = element("input");
    enabled.type = "checkbox"; enabled.checked = rule.enabled; enabled.disabled = !editable || busy;
    enabled.setAttribute("aria-label", `启用 ${rule.name || "未命名规则"}`);
    enabled.addEventListener("change", () => { rule.enabled = enabled.checked; updateDraft(); });
    const label = element("label", null, "listen-toggle"); label.append(enabled, element("span", "启用"));
    head.append(title, label);
    const sources = element("div", null, "listen-source-chips");
    rule.sources.forEach(ref => {
      const item = dialogs.find(item => item.ref === ref);
      const chip = element("span", null, "listen-chip");
      chip.append(element("span", sourceName(ref, dialogs)), element("small", item?.username ? `@${item.username}` : ref));
      chip.title = ref;
      sources.append(chip);
    });
    const footer = element("div", null, "listen-rule-footer");
    footer.append(element("span", rule.listen_comments ? "频道评论视频 · 实时处理 + 24 小时内每 5 分钟补查" : "收到的全部文件 · 遵守全局过滤条件", "muted"));
    const actions = element("div", null, "listen-rule-actions");
    const edit = button("编辑", () => editRule(index));
    const remove = button("删除", () => { rules.splice(index, 1); renderRules(); updateDraft(); });
    edit.disabled = remove.disabled = !editable || busy;
    actions.append(edit, remove); footer.append(actions);
    card.append(head, sources, footer); host.append(card);
  });
  updateDraft();
}

function ruleID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");
}

function editRule(index) {
  if (!editable || busy) return;
  const draft = index == null ? { id: ruleID(), name: `监听规则 ${rules.length + 1}`, enabled: true, sources: [], listen_comments: false } : cloneRules([rules[index]])[0];
  const root = element("div", null, "listen-editor"), form = element("form");
  const nameLabel = element("label", "规则名称", "listen-field");
  const name = element("input"); name.value = draft.name; name.maxLength = 100; name.setAttribute("aria-label", "规则名称");
  nameLabel.append(name);
  const enabledLabel = element("label", null, "listen-choice"), enabled = element("input");
  enabled.type = "checkbox"; enabled.checked = draft.enabled; enabledLabel.append(enabled, element("span", "启用这条规则"));
  const picker = element("fieldset", null, "listen-picker");
  picker.append(element("legend", "监听来源"));
  const search = element("input"); search.type = "search"; search.placeholder = "搜索名称、@用户名或 ID"; search.setAttribute("aria-label", "监听来源搜索");
  const filter = element("select"); filter.setAttribute("aria-label", "监听来源类型");
  for (const [key, title] of Object.entries({ all: "全部类型", ...sourceKinds })) { const option = element("option", title); option.value = key; filter.append(option); }
  const controls = element("div", null, "listen-picker-controls"); controls.append(search, filter);
  const selected = element("div", null, "listen-source-chips"), results = element("div", null, "listen-picker-results");
  const hint = element("p", catalogError || "只显示当前账号可以访问的聊天；已有选择会保留。", "muted");
  picker.append(controls, selected, results, hint);
  const commentsLabel = element("label", null, "listen-choice"), comments = element("input");
  comments.type = "checkbox"; comments.checked = draft.listen_comments;
  comments.setAttribute("aria-label", "下载频道评论中的视频");
  commentsLabel.append(comments, element("span", "下载频道评论中的视频"));
  const commentsHelp = element("p", "仅对频道帖子生效。原帖发布后的 24 小时内，每 5 分钟补查；之后收到可关联的新评论仍会处理。群组回复已属于群消息下载范围。", "muted");
  function updatePicker() {
    selected.replaceChildren();
    draft.sources.forEach(ref => {
      const remove = button(`${sourceName(ref, dialogs)} ×`, () => { draft.sources = draft.sources.filter(source => source !== ref); updatePicker(); }, "listen-chip");
      remove.setAttribute("aria-label", `移除 ${sourceName(ref, dialogs)}`); selected.append(remove);
    });
    results.replaceChildren();
    const matches = matchingDialogs(dialogs, search.value, filter.value);
    matches.slice(0, 100).forEach(item => {
      const label = element("label", null, "listen-chat-choice"), check = element("input");
      check.type = "checkbox"; check.checked = draft.sources.includes(item.ref); check.setAttribute("aria-label", `选择 ${item.title || item.ref}`);
      check.addEventListener("change", () => { draft.sources = check.checked ? [...new Set([...draft.sources, item.ref])] : draft.sources.filter(ref => ref !== item.ref); updatePicker(); });
      const text = element("span"); text.append(element("strong", item.title || item.ref), element("small", `${item.username ? `@${item.username} · ` : ""}${item.ref}`));
      label.append(check, text, element("small", sourceKinds[item.kind] || item.kind)); results.append(label);
    });
    if (!matches.length) results.append(element("p", dialogs.length ? "没有匹配的聊天。" : "聊天列表尚未加载，可关闭后刷新聊天列表。", "empty"));
    if (matches.length > 100) results.append(element("p", `显示前 100 项，共 ${matches.length} 项；输入名称继续筛选。`, "muted"));
    comments.disabled = !hasChannel(draft.sources, dialogs);
    if (comments.disabled) comments.checked = false;
  }
  search.addEventListener("input", updatePicker); filter.addEventListener("change", updatePicker);
  const error = element("p", "", "notice error"); error.hidden = true; error.setAttribute("role", "alert");
  const actions = element("div", null, "listen-editor-actions");
  const cancel = button("取消", closeDrawer), done = element("button", "完成编辑", "btn primary"); done.type = "submit";
  actions.append(cancel, done);
  form.append(nameLabel, enabledLabel, picker, commentsLabel, commentsHelp, error, actions);
  form.addEventListener("submit", event => {
    event.preventDefault();
    draft.name = name.value.trim(); draft.enabled = enabled.checked; draft.listen_comments = comments.checked;
    const message = validateRules([draft]);
    error.hidden = !message; error.textContent = message;
    if (message) return;
    if (index == null) rules.push(draft); else rules[index] = draft;
    closeDrawer(); renderRules(); report("");
  });
  root.append(form); updatePicker(); openDrawer(index == null ? "添加监听规则" : "编辑监听规则", root);
}

async function refreshDialogs() {
  const version = generation;
  dialogRequest ||= api("/api/dialogs");
  try {
    const data = await dialogRequest;
    if (!active || version !== generation) return;
    dialogs = data.items || []; catalogError = ""; renderRules();
    if (lastStatus) renderStatus(lastStatus);
  } catch (error) {
    if (!active || version !== generation) return;
    catalogError = `聊天列表加载失败：${error.message}`; report(catalogError);
  } finally { dialogRequest = null; }
}

function renderStatus(data) {
  const service = $("listen-service-status");
  const listening = data.connected && data.download_enabled && data.sources?.length > 0;
  service.className = `notice ${listening ? "success" : ""}`;
  service.textContent = listening ? "正在监听 · 收到更新后立即处理 · 评论每 5 分钟补查，原帖满 24 小时停止补查" :
    !data.download_enabled ? "等待下载触发服务启用 · 规则可以保存，请在模块管理启用下载触发并按提示重启。" : "等待 Telegram 连接 · 连接恢复后最多补齐最近 24 小时的漏消息。";
  if (data.connected && data.download_enabled && !listening) service.textContent = "暂无启用的监听来源 · 历史任务继续更新。";
  const filters = data.filters || {}, parts = [];
  if (filters.include?.length) parts.push(`允许扩展名：${filters.include.join("、")}`);
  if (filters.exclude?.length) parts.push(`排除扩展名：${filters.exclude.join("、")}`);
  parts.push(`文件大小：${filters.min_mb || 0} MB 起，${filters.max_mb ? `最大 ${filters.max_mb} MB` : "上限不限"}`);
  if (filters.limit) parts.push(`共享下载上限：${filters.limit} 个文件`);
  if (filters.dc_connections) parts.push(`每个 DC ${filters.dc_connections} 个连接`);
  parts.push("表情触发优先，进行中的下载继续运行");
  const filterHost = $("listen-filters"); filterHost.replaceChildren(element("span", `当前生效的过滤条件 · ${parts.join(" · ")}`));
  const link = element("a", "调整下载设置"); link.href = "/config?tab=download"; link.dataset.appLink = ""; filterHost.append(link);
  const sources = data.sources || [], counts = { "监听来源": sources.length, "已提交": sources.reduce((sum, source) => sum + (source.queued || 0), 0), "已过滤": sources.reduce((sum, source) => sum + (source.filtered || 0), 0), "待处理": data.pending || 0, "待确认": data.uncertain || 0, "跟踪评论区": data.tracking || 0 };
  $("listen-metrics").replaceChildren();
  for (const [title, value] of Object.entries(counts)) { const card = element("div", null, "listen-metric"); card.append(element("span", title), element("strong", String(value))); $("listen-metrics").append(card); }
  const host = $("listen-sources"); host.replaceChildren();
  const groups = listenSourceGroups(sources, listenTasks(data.tasks || [], downloadTasks), component?.values?.rules || []);
  groups.forEach(source => {
    const row = element("article", null, "listen-source-row");
    row.dataset.source = source.ref;
    row.append(element("h3", source.info?.title && source.info.title !== source.ref ? source.info.title : sourceName(source.ref, dialogs)), element("small", source.ref));
    if (!source.enabled) row.append(element("p", "已停止监听 · 保留历史任务"));
    row.append(element("p", `最近消息：${source.last_message_at ? formatTime(source.last_message_at * 1000) : "等待新消息"} · 最近补齐：${validTime(source.last_scan_at)}`));
    row.append(element("p", `最近评论补查：${validTime(source.last_comment_scan_at)}`));
    row.append(element("p", `已提交 ${source.queued || 0} · 已过滤 ${source.filtered || 0} · 提交异常 ${source.failed || 0}`));
    if (source.last_error) row.append(element("p", source.last_error, "notice error"));
    renderTasks(row, source.tasks);
    host.append(row);
  });
  if (!groups.length) host.append(element("p", "保存并启用规则后，来源状态与任务将在这里显示。", "empty"));
  $("listen-task-error").hidden = !downloadError;
  $("listen-task-error").textContent = downloadError ? `下载状态刷新失败：${downloadError}。${downloadTasks.length ? "当前显示上次取得的状态。" : "当前仅显示监听提交状态。"}` : "";
  if (data.last_error) { service.className = "notice error"; service.textContent = data.last_error; }
}

function renderTasks(host, tasks) {
  const title = element("h4", `全部任务 · ${tasks.length}`);
  host.append(title);
  if (!tasks.length) { host.append(element("p", "还没有下载任务。", "empty")); return; }
  const list = element("div", null, "listen-task-list"); list.setAttribute("role", "list");
  for (const task of tasks) {
    const row = element("div", null, "listen-task"); row.setAttribute("role", "listitem"); row.dataset.taskId = task.task_id;
    const head = element("div", null, "listen-task-head");
    const name = element("strong", task.file_name); name.title = task.file_name;
    const label = ({ pending: "待处理", submitting: "正在提交", accepted: "已提交", filtered: "已过滤", uncertain: "待确认", obsolete: "已停止", failed: "提交失败" })[task.state] || statusLabel(task.state);
    const badge = element("span", label, `badge ${statusClass(task.state)}`);
    head.append(name, badge); row.append(head);
    row.append(element("small", `${task.comment ? "频道评论视频" : "消息文件"} · 消息 #${task.message_id} · ${task.date ? formatTime(task.date * 1000) : "—"}${task.executor ? ` · ${task.executor === "local" ? "本地下载" : "aria2"}` : ""}`));
    const percent = task.total > 0 ? Math.min(100, Math.max(0, task.completed / task.total * 100)) : 0;
    const progress = element("progress"); progress.max = 100; progress.value = percent; progress.setAttribute("aria-label", `${task.file_name} 下载进度`);
    const details = element("div", null, "listen-task-progress");
    details.append(progress, element("span", `${percent.toFixed(1)}% · ${formatBytes(task.completed)} / ${task.total ? formatBytes(task.total) : "大小未知"}${task.download_speed > 0 ? ` · ${formatBytes(task.download_speed)}/s` : ""}`));
    row.append(details);
    if (task.error) row.append(element("p", task.error, "notice error"));
    list.append(row);
  }
  host.append(list);
}

function validTime(value) { return !value || value.startsWith("0001-") ? "尚未补齐" : formatTime(value); }

async function refreshStatus() {
  if (statusRequest) return statusRequest;
  const version = generation;
  const call = (async () => {
  try {
    const [status, transfers] = await Promise.allSettled([api("/api/listen-downloads/status"), api("/api/download-tasks")]);
    if (!active || version !== generation) return;
    if (transfers.status === "fulfilled") { downloadTasks = transfers.value.items || []; downloadError = ""; }
    else downloadError = transfers.reason.message;
    if (status.status === "rejected") throw status.reason;
    statusError = ""; lastStatus = status.value; renderStatus(lastStatus);
  } catch (error) {
    if (!active || version !== generation) return;
    statusError = error.message;
    $("listen-service-status").className = "notice error";
    $("listen-service-status").textContent = `监听状态暂不可用：${statusError}`;
  }
  })();
  statusRequest = call;
  try { await call; } finally { if (statusRequest === call) statusRequest = null; }
}

async function save() {
  if (busy || !dirty()) return;
  const invalid = validateRules(rules);
  if (invalid) { report(invalid); return; }
  busy = true; renderRules();
  try {
    const data = await api("/api/components", { method: "PATCH", body: JSON.stringify({ id: ID, revision: component.revision || "", values: { rules: cloneRules(rules) } }) });
    const saved = data.components?.find(item => item.id === ID);
    if (!saved) throw new Error("保存响应缺少监听规则，请刷新核对。");
    component = saved; rules = cloneRules(saved.values?.rules);
    report(saved.state === "running" ? "监听规则已保存并立即生效。" : "监听规则已保存，等待监听服务启动。", true);
    window.dispatchEvent(new CustomEvent("configuration-changed", { detail: { id: ID } }));
    await refreshStatus();
  } catch (error) { report(error.status === 409 ? "配置已被其他页面修改，草稿已保留。请刷新页面核对后重新保存。" : error.message); }
  finally { busy = false; renderRules(); }
}

function init() {
  $("listen-add").addEventListener("click", () => editRule());
  $("listen-save").addEventListener("click", save);
  $("listen-discard").addEventListener("click", () => { rules = cloneRules(component.values?.rules); renderRules(); report(""); });
  $("listen-refresh-dialogs").addEventListener("click", () => { dialogRequest = null; void refreshDialogs(); });
}

async function load() {
  active = true; const version = ++generation; clearInterval(timer);
  const data = await api("/api/components");
  if (!active || version !== generation) return;
  const next = data.components?.find(item => item.id === ID);
  if (!next) throw new Error("监听下载组件不可用。");
  if (!component || !dirty()) { component = next; rules = cloneRules(next.values?.rules); }
  editable = data.editable !== false;
  renderRules();
  void refreshDialogs();
  await refreshStatus();
  if (active && version === generation) timer = setInterval(() => { void refreshStatus(); }, 5000);
}

function stop() { active = false; ++generation; clearInterval(timer); closeDrawer(); }
export const page = { init, load, stop, dirty };

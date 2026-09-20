import { api } from "./api.js";
import { element, button } from "./ui.js";
import { confirmReset } from "./system-reset.js";

export async function renderSystem(host, context) {
  const { configData, onChanged, markDirty, busy, data } = context;
  const account = element("section", null, "settings-block");
  account.id = "setting-system-namespace";
  account.tabIndex = -1;
  const link = element("a", "前往账号管理");
  link.href = "/user";
  link.dataset.appLink = "";
  account.append(
    element("h2", "当前账号数据空间"),
    element("p", configData.config?.namespace || "—", "settings-namespace"),
    element(
      "p",
      "每个数据空间保存各自的登录会话与历史任务；切换账号在账号管理中进行，需要重启 TDL。代理、下载规则等设置由所有账号共用。",
      "subtle",
    ),
    link,
  );
  host.append(account);

  const form = element("form", null, "settings-block"),
    label = element("label", "记录详细日志"),
    input = element("input"),
    control = element("div", null, "settings-input settings-switch"),
    state = element("span", null, "settings-switch-state"),
    row = element("div", null, "settings-field"),
    help = element("div", null, "settings-help");
  input.type = "checkbox";
  input.id = "setting-system-debug";
  input.setAttribute("role", "switch");
  input.setAttribute("aria-describedby", "setting-system-debug-help");
  label.htmlFor = input.id;
  state.setAttribute("aria-hidden", "true");
  let baseline = Boolean(configData.config?.debug);
  input.checked = context.getDraft?.() ?? baseline;
  const save = button("保存此区块", () => {}, "btn primary");
  save.type = "submit";
  const reset = button("撤销修改", () => {
    input.checked = baseline;
    status.textContent = "";
    markDirty(undefined);
  });
  input.addEventListener("change", () => {
    status.textContent = "";
    markDirty(input.checked === baseline ? undefined : input.checked);
  });
  const status = element("p", "", "notice");
  status.setAttribute("role", "status");
  help.id = "setting-system-debug-help";
  help.append(
    element(
      "p",
      "排查问题时开启，记录更多运行细节；平时保持关闭。保存后在顶部重启应用才会生效。",
    ),
    element("small", "默认：关闭", "settings-field-meta"),
  );
  control.append(input, state);
  row.append(label, control, help);
  const actions = element("div", null, "settings-block-actions");
  actions.append(save, reset);
  form.append(element("h2", "运行日志"), row, actions, status);
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (save.disabled) return;
    context.setBusy(true);
    try {
      const result = await api("/api/config", {
        method: "PATCH",
        body: JSON.stringify({ values: { debug: input.checked } }),
      });
      baseline = Boolean(result.config.debug);
      configData.config = result.config;
      input.checked = baseline;
      markDirty(undefined);
      status.textContent = "日志设置已保存到 tdl_config.json，重启后生效。";
      status.className = "notice success";
      onChanged(result);
    } catch (error) {
      status.textContent = `保存失败，修改已保留：${error.message}`;
      status.className = "notice error";
    } finally {
      context.setBusy(false);
    }
  });
  host.append(form);

  const maintenance = element(
    "section",
    null,
    "settings-block settings-maintenance",
  );
  maintenance.append(element("h2", "应用维护"));
  const resetAll = button(
    "完全重置…",
    () => {
      if (!busy.size) void confirmReset(context);
    },
    "btn danger",
  );
  for (const [title, description, action] of [
    [
      "完全重置",
      "删除全局配置，以及所有账号的登录会话、任务记录和日志。此操作不可撤销。",
      resetAll,
    ],
  ]) {
    const row = element("div", null, "settings-maintenance-row"),
      text = element("div");
    text.append(element("h3", title), element("p", description, "subtle"));
    row.append(text, action);
    maintenance.append(row);
  }
  host.append(maintenance);
  function sync() {
    const locked = busy.size > 0 || !configData.editable || !data.editable,
      changed = input.checked !== baseline;
    input.disabled = locked;
    save.disabled = locked || !changed;
    reset.disabled = locked || !changed;
    resetAll.disabled = locked;
    save.textContent = busy.has("runtime") ? "正在处理…" : "保存此区块";
    state.textContent = input.checked ? "已开启" : "已关闭";
    form.classList.toggle("has-drafts", changed);
  }
  sync();
  return { sync };
}

import { api } from "./api.js";
import { element, button } from "./ui.js";
import { confirmReset } from "./system-reset.js";

export async function renderSystem(host, context) {
  const { configData, onChanged, markDirty } = context;
  if (!context.legacy) {
    const form = element("form", null, "settings-block"),
      label = element("label", " 详细日志"),
      input = element("input");
    input.type = "checkbox";
    input.checked = Boolean(configData.config?.debug);
    label.prepend(input);
    let baseline = input.checked;
    if (context.getDraft?.() !== undefined) input.checked = context.getDraft();
    const save = button("保存更改", () => {}, "btn primary");
    save.type = "submit";
    save.disabled = input.checked === baseline;
    const reset = button("撤销修改", () => {
      input.checked = baseline;
      save.disabled = true;
      markDirty(undefined);
    });
    input.addEventListener("change", () => {
      save.disabled = input.checked === baseline;
      markDirty(input.checked === baseline ? undefined : input.checked);
    });
    const status = element("p", "", "notice");
    status.setAttribute("role", "status");
    form.append(
      element("h2", "运行选项"),
      label,
      element("p", "排查问题时开启详细日志，平时保持关闭。", "subtle"),
    );
    const actions = element("div", null, "settings-block-actions");
    actions.append(save, reset);
    form.append(actions, status);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      save.disabled = true;
      input.disabled = true;
      reset.disabled = true;
      context.setBusy?.(true);
      try {
        const result = await api("/api/config", {
          method: "PATCH",
          body: JSON.stringify({ values: { debug: input.checked } }),
        });
        baseline = Boolean(result.config.debug);
        configData.config = result.config;
        input.checked = baseline;
        markDirty(undefined);
        status.textContent = "运行选项已保存。";
        onChanged();
      } catch (error) {
        status.textContent = error.message;
        save.disabled = false;
      } finally {
        input.disabled = false;
        reset.disabled = false;
        context.setBusy?.(false);
      }
    });
    host.append(form);
  }
  const reboot = button(
    "重启 TDL",
    async () => {
      if (!confirm("确认重启 TDL？未保存设置将丢失，当前连接会暂时断开。"))
        return;
      reboot.disabled = true;
      try {
        await api("/api/system/reboot", { method: "POST", body: "{}" });
        reboot.textContent = "正在重启…";
      } catch (error) {
        reboot.textContent = `重启失败：${error.message}`;
        reboot.disabled = false;
      }
    },
    "btn danger",
  );
  host.append(reboot);
  host.append(
    button(
      "一键完全重置",
      () => {
        void confirmReset(context);
      },
      "btn danger",
    ),
  );
}

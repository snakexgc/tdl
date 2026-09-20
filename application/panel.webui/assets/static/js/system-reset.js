import { api } from "./api.js";
import { element, button } from "./ui.js";
import { stopRealtime } from "./events.js";
import { stopPages } from "./router.js";

export async function confirmReset(context) {
  const origin = document.activeElement;
  const dialog = element("dialog", null, "reset-dialog");
  dialog.setAttribute("role", "alertdialog");
  dialog.setAttribute("aria-labelledby", "reset-heading");
  dialog.setAttribute("aria-describedby", "reset-warning");
  const heading = element("h2", "确认一键完全重置？");
  heading.id = "reset-heading";
  const warning = element(
    "p",
    "此操作不可撤销。将永久删除全局配置、代理与密钥，以及所有命名空间的 Telegram 登录凭据、任务记录、日志和 .tdl 文件夹中的所有内容。",
    "reset-warning",
  );
  warning.id = "reset-warning";
  const explanation = element(
    "p",
    "tdl_config.json、旧 config.json 和 components 配置目录位于 .tdl 外，也会一并清理。完成后 TDL 将退出，下次启动需要重新配置并登录。",
    "subtle",
  );
  const targets = element("ul", null, "reset-targets");
  const status = element("p", "正在确认清理范围…", "notice");
  status.setAttribute("role", "status");
  const actions = element("div", null, "reset-actions");
  let pending = false,
    accepted = false;
  const cancel = button("取消", () => dialog.close());
  const confirm = button(
    "确认完全重置",
    async () => {
      if (pending || accepted) return;
      pending = true;
      cancel.disabled = confirm.disabled = true;
      context.setBusy?.(true);
      status.textContent = "正在提交重置请求…";
      status.className = "notice";
      try {
        const result = await api("/api/system/reset", {
          method: "POST",
          body: JSON.stringify({ confirmation: "RESET_TDL" }),
        });
        accepted = true;
        context.onResetAccepted?.();
        stopRealtime();
        stopPages();
        heading.textContent = "正在完全重置";
        status.textContent = result.message;
        warning.hidden =
          explanation.hidden =
          targets.hidden =
          actions.hidden =
            true;
      } catch (error) {
        status.textContent = error.message;
        status.className = "notice error";
        cancel.disabled = confirm.disabled = false;
      } finally {
        pending = false;
        if (!accepted) context.setBusy?.(false);
      }
    },
    "btn danger",
  );
  confirm.disabled = true;
  actions.append(cancel, confirm);
  dialog.append(heading, warning, explanation, targets, status, actions);
  dialog.addEventListener("cancel", (event) => {
    if (pending || accepted) event.preventDefault();
  });
  const leave = () => {
    if (!pending && !accepted) dialog.close();
  };
  window.addEventListener("page-changing", leave);
  dialog.addEventListener(
    "close",
    () => {
      dialog.remove();
      window.removeEventListener("page-changing", leave);
      if (origin?.isConnected) origin.focus();
    },
    { once: true },
  );
  document.body.append(dialog);
  dialog.showModal();
  cancel.focus();
  try {
    const result = await api("/api/system/reset");
    if (!dialog.isConnected) return;
    for (const path of result.targets) targets.append(element("li", path));
    status.textContent = "请确认以上数据不再需要后再继续。";
    confirm.disabled = false;
  } catch (error) {
    if (dialog.isConnected) {
      status.textContent = error.message;
      status.className = "notice error";
    }
  }
}

import { api } from "./api.js";

export async function cleanStorage(button, { refresh, notify }) {
  if (button.disabled) return;
  button.disabled = true;
  const label = button.textContent;
  try {
    const { namespace } = await api("/api/status");
    if (!button.isConnected) return;
    if (!namespace) throw new Error("无法确认当前账号，请刷新页面后重试。");
    if (!confirm(
      `确认清理账号「${namespace}」的存储记录？\n\n` +
      "将删除下载链接、下载与转发任务等记录，不限于过期缓存。此操作不可撤销，可能影响任务恢复和已有下载链接，建议先停止相关任务。\n\n" +
      "保留 Telegram 登录信息与同步状态，不删除已下载文件，也不影响其他账号。",
    )) return;
    button.textContent = "正在清理…";
    notify("正在清理存储记录…");
    const result = await api("/api/storage/clean", {
      method: "POST",
      body: JSON.stringify({ confirmation: "CLEAN_STORAGE", namespace }),
    });
    if (!button.isConnected) return;
    await refresh();
    if (!button.isConnected) return;
    const errors = result.errors || [];
    const failed = result.ok === false || errors.length > 0;
    const detail = errors.length ? `；错误：${errors.slice(0, 5).join("；")}` : "";
    notify(
      `账号「${result.namespace}」${failed ? "清理未全部完成" : "清理完成"}：已删除 ${result.deleted || 0} 条，保留 ${result.kept || 0} 条${detail}。`,
      failed ? "error" : "success",
    );
  } catch (error) {
    if (button.isConnected) notify(`存储清理失败：${error.message}`, "error");
  } finally {
    button.disabled = false;
    button.textContent = label;
  }
}

import { element } from "./ui.js";

// The dedicated page is the only rule editor; advanced settings link to it.
export async function createEditor(value) {
  const root = element("div", null, "listen-setting-link");
  const count = Array.isArray(value) ? value.length : 0;
  root.append(element("p", `已配置 ${count} 条监听规则，保存后立即生效。`));
  const link = element("a", "管理监听规则", "btn secondary");
  link.href = "/listen-downloads";
  link.dataset.appLink = "";
  root.append(link);
  return { element: root, value: () => structuredClone(value || []) };
}

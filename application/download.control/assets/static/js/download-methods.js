import { element } from "./ui.js";

const methods = [
  ["local", "本地下载器", "保存到 TDL 所在机器，不启动 aria2 管理。"],
  ["aria2", "aria2 下载器", "自动向 aria2 添加下载任务，保存到 aria2 所在机器。"],
];

export async function createEditor(value, { editable }) {
  const root = element("div", null, "download-methods");
  // Keep legacy link-only settings intact until the user chooses a downloader.
  let selected = value.find((name) => name === "local" || name === "aria2");
  function render(focus) {
    root.replaceChildren();
    for (const [name, title, description] of methods) {
      const row = element("div", null, "download-method"),
        label = element("label"),
        input = element("input"),
        text = element("span");
      input.type = "radio";
      input.name = "download-method";
      input.checked = selected === name;
      input.disabled = !editable;
      input.dataset.method = name;
      input.setAttribute("aria-label", title);
      input.addEventListener("change", () => {
        if (!editable || !input.checked) return;
        selected = name;
        render(name);
        root.dispatchEvent(new Event("change", { bubbles: true }));
      });
      text.append(element("strong", title), element("small", description, "subtle"));
      label.append(input, text);
      row.classList.toggle("is-selected", selected === name);
      row.append(label);
      root.append(row);
    }
    if (!selected)
      root.append(element("p", "当前沿用仅生成链接的旧配置；选择下载器后启用自动下载。", "subtle"));
    if (focus) root.querySelector(`[data-method="${focus}"]`).focus();
  }
  render();
  return { element: root, value: () => selected ? [selected, "http"] : ["http"] };
}

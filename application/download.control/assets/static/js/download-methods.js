import { element, button } from "./ui.js";

const methods = [
  [
    "aria2",
    "交给 aria2 下载",
    "保存到 aria2 所在机器；需要配置下方的连接地址并开启自动提交。",
  ],
  [
    "local",
    "保存到本机",
    "保存到 TDL 所在机器；目录留空时使用程序所在目录下的 download 文件夹。",
  ],
  [
    "http",
    "仅生成下载链接",
    "交给其他下载工具处理；需要在「下载链接」中配置访问地址。",
  ],
];

export async function createEditor(value, { editable }) {
  const root = element("div", null, "download-methods");
  let selected = [...value];
  function render(focus) {
    root.replaceChildren();
    const order = [
      ...selected.filter((name) => name !== "http"),
      ...methods
        .map(([name]) => name)
        .filter((name) => name !== "http" && !selected.includes(name)),
      "http",
    ];
    for (const name of order) {
      const [, title, description] = methods.find(([key]) => key === name),
        index = selected.indexOf(name),
        row = element("div", null, "download-method"),
        label = element("label"),
        input = element("input"),
        text = element("span"),
        actions = element("div", null, "download-method-actions");
      input.type = "checkbox";
      input.checked = index >= 0;
      input.disabled = !editable;
      input.dataset.method = name;
      input.setAttribute("aria-label", title);
      if (name === order[0] && !selected.length)
        input.setCustomValidity("请至少选择一种下载方式。");
      input.addEventListener("change", () => {
        selected = selected.filter((key) => key !== name);
        if (input.checked) {
          const last = selected.indexOf("http");
          selected.splice(last < 0 ? selected.length : last, 0, name);
        }
        render(name);
        root.dispatchEvent(new Event("change", { bubbles: true }));
      });
      text.append(
        element("strong", title),
        element("small", description, "subtle"),
      );
      label.append(input, text);
      const priority = element(
        "span",
        index < 0 ? "未选择" : `第 ${index + 1} 顺位`,
        "subtle",
      );
      actions.append(priority);
      if (name !== "http") {
        for (const [offset, caption] of [
          [-1, "上移"],
          [1, "下移"],
        ]) {
          const move = button(caption, () => {
            [selected[index], selected[index + offset]] = [
              selected[index + offset],
              selected[index],
            ];
            render(name);
            root.dispatchEvent(new Event("change", { bubbles: true }));
          });
          move.setAttribute("aria-label", `${caption}${title}`);
          move.disabled =
            !editable ||
            index < 0 ||
            index + offset < 0 ||
            index + offset >= selected.length ||
            selected[index + offset] === "http";
          actions.append(move);
        }
      } else actions.append(element("span", "始终最后", "subtle"));
      row.classList.toggle("is-selected", index >= 0);
      row.append(label, actions);
      root.append(row);
    }
    if (!selected.length)
      root.append(
        element("p", "请至少选择一种下载方式。", "settings-field-error"),
      );
    if (focus) root.querySelector(`[data-method="${focus}"]`).focus();
  }
  render();
  return { element: root, value: () => [...selected] };
}

import { element, button } from "./ui.js";
import { createProxyControl } from "./proxy-control.js";

export async function renderSettingsBlock(host, group, context) {
  const { store, editable, busy, update, save } = context;
  const names = group.fields.map((field) => field.name);
  let form,
    controls,
    saveButton,
    resetButton,
    notice,
    editors = [];
  const report = (text, kind = "") => {
    notice.textContent = text;
    notice.className = `notice ${kind}`;
  };
  async function draw() {
    editors.forEach((editor) => editor.dispose?.());
    editors = [];
    form = element("form", null, "settings-block");
    form.dataset.component = group.id;
    const head = element("header", null, "settings-block-head");
    head.append(
      element("h2", group.title),
      element(
        "p",
        store.components.get(group.id).enabled === false
          ? "服务已停用，仍可保存配置。"
          : "",
        "subtle",
      ),
    );
    if (store.components.get(group.id).enabled === false) {
      const link = element("a", "管理服务");
      link.href = "/modules";
      link.dataset.appLink = "";
      head.append(link);
    }
    form.append(head);
    controls = element("fieldset", null, "settings-fields");
    controls.disabled = !editable;
    form.append(controls);
    const advanced = element("details", null, "advanced-fields");
    advanced.append(element("summary", "高级选项"));
    for (const field of group.fields) {
      const row = element(
        "div",
        null,
        field.editor ? "settings-editor-field" : "settings-field",
      );
      const title = element("label", field.title || field.name),
        controlID = `setting-${group.id}-${field.name}`;
      title.htmlFor = controlID;
      const help = element(
        "small",
        field.help ||
          (field.secret
            ? "留空保留已保存的值。"
            : field.restart_required
              ? "保存后自动重新配置对应服务。"
              : ""),
        "subtle",
      );
      help.id = `${controlID}-help`;
      const current = store.value(group.id, field.name);
      row.append(title);
      if (field.editor) {
        const url = new URL(field.editor, window.location.origin);
        if (
          url.origin !== window.location.origin ||
          !url.pathname.startsWith("/static/js/")
        )
          throw new Error("无效的配置编辑器路径。");
        const editor = await (
          await import(url.pathname)
        ).createEditor(current, { editable });
        editors.push(editor);
        editor.element.id = controlID;
        row.append(editor.element);
        editor.element.addEventListener("input", () => {
          store.set(group.id, field.name, editor.value());
          update();
        });
        editor.element.addEventListener("change", () => {
          store.set(group.id, field.name, editor.value());
          update();
        });
      } else if (field.format === "proxy") {
        row.append(
          createProxyControl(current, {
            id: controlID,
            helpID: help.id,
            onChange: (value) => {
              store.set(group.id, field.name, value);
              update();
            },
          }),
        );
      } else {
        const input = element(
          field.choices?.length
            ? "select"
            : field.type === "strings"
              ? "textarea"
              : "input",
        );
        input.id = controlID;
        input.name = field.name;
        input.setAttribute("aria-describedby", help.id);
        if (field.choices?.length) {
          for (const choice of field.choices) {
            const option = element(
              "option",
              field.choice_labels?.[choice] ?? choice,
            );
            option.value = choice;
            input.append(option);
          }
          input.value = String(current ?? "");
        } else if (field.type === "bool") {
          input.type = "checkbox";
          input.checked = Boolean(current);
        } else if (field.type === "int") {
          input.type = "number";
          input.step = "1";
          input.required = !field.empty_preserves;
          if (field.min != null) input.min = String(field.min);
          if (field.max != null) input.max = String(field.max);
          input.value =
            field.empty_preserves && !current ? "" : String(current ?? 0);
          if (field.empty_preserves) input.placeholder = "留空保持不变";
        } else if (field.type === "strings") {
          input.value = Array.isArray(current) ? current.join("\n") : "";
          input.placeholder = "每行一项";
        } else {
          input.type = field.secret ? "password" : "text";
          input.value = String(current ?? "");
          if (field.secret) {
            input.autocomplete = "new-password";
            input.placeholder = "留空保持不变";
          }
        }
        const changed = () => {
          const value =
            field.type === "bool"
              ? input.checked
              : field.type === "int"
                ? field.empty_preserves && input.value === ""
                  ? ""
                  : Number(input.value)
                : field.type === "strings"
                  ? input.value
                      .split(/\r?\n/)
                      .map((v) => v.trim())
                      .filter(Boolean)
                  : input.value;
          store.set(group.id, field.name, value);
          update();
        };
        input.addEventListener("input", changed);
        input.addEventListener("change", changed);
        row.append(input);
      }
      row.append(help);
      if (!field.secret && store.drafts.get(group.id)?.has(field.name))
        row.append(
          element(
            "small",
            `服务端当前值：${JSON.stringify(store.baseline(group.id, field.name))}`,
            "server-value",
          ),
        );
      (field.advanced ? advanced : controls).append(row);
    }
    if (advanced.children.length > 1) controls.append(advanced);
    const footer = element("div", null, "settings-block-actions");
    saveButton = button("保存更改", () => {}, "btn primary");
    saveButton.type = "submit";
    resetButton = button("撤销修改", async () => {
      store.reset(group.id, names);
      await draw();
      update();
    });
    footer.append(saveButton, resetButton);
    notice = element("p", null, "notice");
    notice.setAttribute("role", "status");
    form.append(footer, notice);
    form.noValidate = true;
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      const invalid = form.querySelector(
        "input:invalid, select:invalid, textarea:invalid",
      );
      const advanced = invalid?.closest("details");
      if (advanced) advanced.open = true;
      if (form.reportValidity()) void save(group, report, draw);
    });
    host.replaceChildren(form);
    sync();
  }
  function sync() {
    const saving = busy.has(group.id),
      changed = Object.keys(store.patch(group.id, names)).length > 0;
    controls.disabled = !editable || saving;
    saveButton.disabled = !editable || saving || !changed;
    resetButton.disabled = saving || !changed;
  }
  await draw();
  return {
    sync,
    dispose: () => editors.forEach((editor) => editor.dispose?.()),
  };
}

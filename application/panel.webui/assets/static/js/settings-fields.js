import { element, button } from "./ui.js";
import { createProxyControl } from "./proxy-control.js";
import {
  settingID,
  displayValue,
  fieldHint,
  settingsErrors,
} from "./settings-model.js";

export async function renderSettingsBlock(host, group, context) {
  const { store, editable, busy, update, save } = context;
  const names = group.fields.map((field) => field.name);
  let form,
    controls,
    saveButton,
    resetButton,
    notice,
    draftStatus,
    editors = [],
    rows = [],
    submitted = false,
    listeners = new AbortController();
  const report = (text, kind = "") => {
    notice.textContent = text;
    notice.className = `notice ${kind}`;
  };
  const changed = (field, value) => {
    store.set(group.id, field.name, value);
    report("");
    update();
  };
  async function draw() {
    listeners.abort();
    listeners = new AbortController();
    editors.forEach((editor) => editor.dispose?.());
    editors = [];
    rows = [];
    submitted = false;
    form = element("form", null, "settings-block");
    form.dataset.component = group.id;
    form.setAttribute("aria-label", group.title);
    const head = element("header", null, "settings-block-head");
    head.append(element("h2", group.title));
    if (store.components.get(group.id).enabled === false) {
      const status = element(
        "p",
        "此服务的已保存状态为停用。配置和启停修改均在重启后生效。 ",
        "subtle",
      );
      const link = element("a", "管理功能开关");
      link.href = "/modules";
      link.dataset.appLink = "";
      status.append(link);
      head.append(status);
    }
    form.append(head);
    controls = element("fieldset", null, "settings-fields");
    controls.disabled = !editable;
    form.append(controls);
    for (const field of group.fields) {
      const row = element(
          "div",
          null,
          field.editor ? "settings-editor-field" : "settings-field",
        ),
        controlID = settingID(group.id, field.name),
        title = element("label", field.title || field.name),
        control = element("div", null, "settings-input"),
        help = element("div", null, "settings-help"),
        error = element("small", null, "settings-field-error"),
        saved = element("small", null, "server-value");
      title.htmlFor = controlID;
      title.id = `${controlID}-label`;
      help.id = `${controlID}-help`;
      error.id = `${controlID}-error`;
      error.hidden = true;
      error.setAttribute("aria-live", "polite");
      if (field.help) help.append(element("p", field.help));
      const preview = store.components.get(group.id).previews?.[field.name];
      if (preview) help.prepend(element("p", `配置文件中的地址：${preview}`));
      help.append(element("small", fieldHint(field), "settings-field-meta"));
      const current = store.value(group.id, field.name);
      const record = { field, row, error, saved, touched: false };
      rows.push(record);
      row.append(title, control, help, error, saved);
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
        editor.element.tabIndex = -1;
        editor.element.setAttribute("role", "group");
        editor.element.setAttribute("aria-labelledby", title.id);
        editor.element.setAttribute("aria-describedby", help.id);
        control.append(editor.element);
        for (const event of ["input", "change"])
          editor.element.addEventListener(
            event,
            () => changed(field, editor.value()),
            { signal: listeners.signal },
          );
      } else if (field.format === "proxy") {
        const proxy = createProxyControl(current, {
          id: controlID,
          helpID: help.id,
          onChange: (value) => changed(field, value),
        });
        record.input = proxy.querySelector("[data-proxy-address]");
        record.input.setAttribute("aria-describedby", `${help.id} ${error.id}`);
        record.input.addEventListener("blur", () => {
          record.touched = true;
          sync();
        });
        control.append(proxy);
      } else {
        const input = element(
          field.choices?.length
            ? "select"
            : field.type === "strings"
              ? "textarea"
              : "input",
        );
        record.input = input;
        input.id = controlID;
        input.name = field.name;
        input.setAttribute("aria-describedby", `${help.id} ${error.id}`);
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
          input.setAttribute("role", "switch");
          const state = element(
            "span",
            input.checked ? "已开启" : "已关闭",
            "settings-switch-state",
          );
          state.setAttribute("aria-hidden", "true");
          input.addEventListener("change", () => {
            state.textContent = input.checked ? "已开启" : "已关闭";
          });
          control.classList.add("settings-switch");
          control.append(input, state);
        } else if (field.type === "int") {
          input.type = "number";
          input.step = "1";
          input.required = !field.empty_preserves;
          if (field.min != null) input.min = String(field.min);
          if (field.max != null) input.max = String(field.max);
          input.value =
            field.empty_preserves && !current ? "" : String(current ?? "");
          if (field.empty_preserves) input.placeholder = "留空保留原值";
        } else if (field.type === "strings") {
          input.value = Array.isArray(current) ? current.join("\n") : "";
          input.placeholder = "每行一项";
          input.rows = 3;
          input.spellcheck = false;
        } else {
          input.type = field.secret ? "password" : "text";
          input.value = String(current ?? "");
          input.spellcheck = false;
          input.autocomplete = field.secret ? "new-password" : "off";
          if (field.secret) {
            input.placeholder = "留空保留原值";
            control.classList.add("settings-secret");
            const reveal = button("显示", () => {
              const visible = input.type === "password";
              input.type = visible ? "text" : "password";
              reveal.textContent = visible ? "隐藏" : "显示";
              reveal.setAttribute("aria-pressed", String(visible));
            });
            reveal.setAttribute(
              "aria-label",
              `显示或隐藏新填写的${field.title}`,
            );
            reveal.setAttribute("aria-pressed", "false");
            control.append(input, reveal);
          }
        }
        if (!input.parentElement) control.append(input);
        const onInput = () => {
          const value =
            field.type === "bool"
              ? input.checked
              : field.type === "int"
                ? input.value === ""
                  ? ""
                  : Number(input.value)
                : field.type === "strings"
                  ? input.value
                      .split(/\r?\n/)
                      .map((v) => v.trim())
                      .filter(Boolean)
                  : input.value;
          changed(field, value);
        };
        input.addEventListener("input", onInput);
        input.addEventListener("change", onInput);
        input.addEventListener("blur", () => {
          record.touched = true;
          sync();
        });
      }
      controls.append(row);
    }
    const footer = element("div", null, "settings-block-actions");
    saveButton = button("保存此区块", () => {}, "btn primary");
    saveButton.type = "submit";
    resetButton = button("撤销修改", async () => {
      store.reset(group.id, names);
      await draw();
      update();
    });
    draftStatus = element("span", null, "settings-block-status subtle");
    footer.append(saveButton, resetButton, draftStatus);
    notice = element("p", null, "notice");
    notice.setAttribute("role", "status");
    form.append(footer, notice);
    form.noValidate = true;
    form.addEventListener("submit", (event) => {
      event.preventDefault();
      if (saveButton.disabled) return;
      const invalid = validate();
      if (invalid) {
        for (
          let parent = invalid.parentElement;
          parent;
          parent = parent.parentElement
        )
          if (parent.tagName === "DETAILS") parent.open = true;
        report("请先检查标出的设置，修改尚未保存。", "error");
      }
      if (form.reportValidity()) void save(group, report, draw);
    });
    host.replaceChildren(form);
    sync();
  }
  function sync() {
    const saving = busy.has(group.id),
      locked = saving || busy.has("reload") || busy.has("runtime"),
      patch = store.patch(group.id, names),
      count = Object.keys(patch).length;
    controls.disabled = !editable || locked;
    saveButton.disabled = !editable || busy.size > 0 || !count;
    saveButton.textContent = saving ? "正在保存…" : "保存此区块";
    resetButton.disabled = locked || !count;
    draftStatus.textContent = count ? `${count} 项未保存` : "无未保存修改";
    form.classList.toggle("has-drafts", count > 0);
    const errors = settingsErrors(group.id, (name) =>
      store.value(group.id, name),
    );
    for (const record of rows) {
      const { field, input, row, error, saved } = record,
        isDirty = Object.hasOwn(patch, field.name);
      row.classList.toggle("is-dirty", isDirty);
      saved.hidden = !isDirty || field.secret;
      saved.textContent = saved.hidden
        ? ""
        : `已保存的值：${displayValue(field, store.baseline(group.id, field.name))}`;
      if (!input) continue;
      let problem = errors.get(field.name) || "";
      if (!problem && field.format === "nonempty" && !input.value.trim())
        problem = "此项不能为空。";
      if (!problem && field.format === "url" && input.value !== "") {
        try {
          const url = new URL(input.value);
          if (
            !url.hostname ||
            !["http:", "https:"].includes(url.protocol) ||
            /\s/.test(input.value)
          )
            throw new Error();
        } catch {
          problem = "请填写完整的 http:// 或 https:// 地址，且不能包含空格。";
        }
      }
      if (field.format !== "proxy") input.setCustomValidity(problem);
      if (!problem && !input.validity.valid) {
        if (input.validity.badInput || input.validity.stepMismatch)
          problem = "请输入整数。";
        else if (input.validity.valueMissing)
          problem = "此项不能为空，请填写数值。";
        else if (input.validity.rangeUnderflow)
          problem = `不能小于 ${field.min}。`;
        else if (input.validity.rangeOverflow)
          problem = `不能大于 ${field.max}。`;
        else problem = input.validationMessage;
      }
      const show = Boolean(problem && (submitted || record.touched));
      error.textContent = show ? problem : "";
      error.hidden = !show;
      input.setAttribute("aria-invalid", String(show));
    }
  }
  function validate() {
    if (!Object.keys(store.patch(group.id, names)).length) return null;
    submitted = true;
    sync();
    return form.querySelector(
      "input:invalid, select:invalid, textarea:invalid",
    );
  }
  await draw();
  return {
    sync,
    validate,
    dispose: () => {
      listeners.abort();
      editors.forEach((editor) => editor.dispose?.());
    },
  };
}

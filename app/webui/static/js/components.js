import { api } from "./api.js";

const host = document.getElementById("component-forms");
const message = document.getElementById("component-message");

function render(data) {
  host.replaceChildren();
  document.getElementById("component-mode").textContent = data.editable
    ? "此处保存后立即应用，重启后仍保留。过滤和命名以这里的配置为准。"
    : "当前为只读预览。启用组件配置目录后可在此编辑；当前设置仍由配置文件页面管理。";
  for (const component of data.components) {
    const form = document.createElement("form");
    form.className = "config-section";
    const title = document.createElement("h2");
    title.textContent = component.title || component.id;
    form.append(title);
    const fields = [];
    for (const field of component.fields) {
      const label = document.createElement("label");
      label.style.display = "block";
      label.style.margin = "16px 0";
      const caption = document.createElement("span");
      caption.textContent = field.title || field.name;
      label.append(caption);
      const input = document.createElement(field.type === "strings" ? "textarea" : "input");
      input.name = field.name;
      input.disabled = !data.editable;
      const value = component.values[field.name];
      if (field.type === "bool") {
        input.type = "checkbox";
        input.checked = Boolean(value);
      } else if (field.type === "int") {
        input.type = "number";
        input.step = "1";
        input.required = true;
        if (field.min != null) input.min = String(field.min);
        if (field.max != null) input.max = String(field.max);
        input.value = String(value ?? field.default ?? 0);
      } else if (field.type === "strings") {
        input.value = Array.isArray(value) ? value.join("\n") : "";
        input.placeholder = "每行一项";
      } else {
        input.type = field.secret ? "password" : "text";
        input.value = field.secret ? "" : String(value ?? "");
        if (field.secret) {
          input.autocomplete = "new-password";
          input.placeholder = "留空保留已保存的值";
        }
      }
      label.append(input);
      form.append(label);
      fields.push({ field, input });
    }
    const button = document.createElement("button");
    button.className = "btn primary";
    button.type = "submit";
    button.disabled = !data.editable || component.state !== "running";
    button.textContent = "保存";
    form.append(button);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      const values = {};
      for (const { field, input } of fields) {
        if (field.secret && input.value === "") continue;
        if (field.type === "bool") values[field.name] = input.checked;
        else if (field.type === "int") values[field.name] = Number(input.value);
        else if (field.type === "strings") values[field.name] = input.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean);
        else values[field.name] = input.value;
      }
      button.disabled = true;
      try {
        await api("/api/components", { method: "PATCH", body: JSON.stringify({ id: component.id, values }) });
        message.textContent = "已保存并应用。";
        for (const { field, input } of fields) if (field.secret) input.value = "";
      } catch (error) {
        message.textContent = error.message;
      } finally {
        button.disabled = false;
      }
    });
    host.append(form);
  }
}

api("/api/components").then(render).catch(error => { message.textContent = error.message; });

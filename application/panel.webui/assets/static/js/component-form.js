import { api } from "./api.js";

export async function renderComponentForms(host, message, data, ids) {
  host.replaceChildren();
  const mode = document.getElementById("component-mode");
  if (mode) mode.textContent = data.editable
    ? "配置以此处保存的值为准。标注重启的字段将在对应服务重启后生效；未启动的组件也可编辑。"
    : "当前为只读预览。启用组件配置目录后可在此编辑；当前设置仍由配置文件页面管理。";
  for (const component of data.components) {
    if (ids && !ids.includes(component.id)) continue;
    const form = document.createElement("form");
    form.className = "component-form";
    form.dataset.component = component.id;
    const title = document.createElement("h2");
    title.textContent = component.title || component.id;
    form.append(title);
    const status = document.createElement("p");
    status.textContent = `${component.enabled === false ? "已停用" : "已启用"} · ${component.state}${component.pending_restart ? " · 等待重启生效" : ""}${component.error ? ` · ${component.error}` : ""}`;
    form.append(status);
    const ownMessage = document.createElement("p");
    ownMessage.className = "notice";
    ownMessage.setAttribute("role", "status");
    const report = text => { ownMessage.textContent = text; if (message) message.textContent = text; };
    form.append(ownMessage);
    if (data.can_toggle) {
      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.className = "btn";
      toggle.textContent = component.enabled === false ? "启用组件" : "停用组件";
      toggle.addEventListener("click", async () => {
        toggle.disabled = true;
        try {
          await api("/api/components", { method: "PATCH", body: JSON.stringify({ id: component.id, enabled: !component.enabled, revision: component.revision || "" }) });
          report("启停请求已保存，正在协调依赖。");
          window.dispatchEvent(new CustomEvent("components-changed"));
          await renderComponentForms(host, message, await api("/api/components"), ids);
        } catch (error) {
          report(error.message);
          toggle.disabled = false;
        }
      });
      form.append(toggle);
    }
    for (const page of component.pages || []) {
      if (component.enabled === false) continue;
      const link = document.createElement("a");
      const url = new URL(page.path, window.location.origin);
      if (url.origin !== window.location.origin) continue;
      link.href = url.pathname + url.search + url.hash;
      link.textContent = page.title;
      link.className = "btn";
      form.append(link);
    }
    const fields = [];
    for (const field of component.fields || []) {
      if (field.replaced_by) continue;
      const label = document.createElement("label");
      label.className = "component-field";
      label.style.display = "block";
      label.style.margin = "16px 0";
      const caption = document.createElement("span");
      caption.textContent = `${field.title || field.name}${field.restart_required ? "（自动重启此服务）" : "（热更新）"}`;
      label.append(caption);
      if (field.editor) {
        const url = new URL(field.editor, window.location.origin);
        if (url.origin !== window.location.origin || !url.pathname.startsWith("/static/js/")) throw new Error("无效的配置编辑器路径");
        const editor = await (await import(url.pathname)).createEditor(component.values[field.name], { editable: data.editable });
        const group = document.createElement("div");
        group.append(caption, editor.element);
        form.append(group);
        fields.push({ field, editor });
        continue;
      }
      const input = document.createElement(field.choices?.length ? "select" : field.type === "strings" ? "textarea" : "input");
      input.name = field.name;
      input.disabled = !data.editable;
      const value = component.values[field.name];
      if (field.choices?.length) {
        for (const choice of field.choices) {
          const option = document.createElement("option");
          option.value = choice;
          option.textContent = choice;
          input.append(option);
        }
        input.value = String(value ?? field.default ?? "");
      } else if (field.type === "bool") {
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
    button.disabled = !data.editable || fields.length === 0;
    button.textContent = "保存";
    form.append(button);
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (!form.reportValidity()) return;
      const values = {};
      for (const { field, input, editor } of fields) {
        if (editor) { values[field.name] = editor.value(); continue; }
        if (field.secret && input.value === "") continue;
        if (field.type === "bool") values[field.name] = input.checked;
        else if (field.type === "int") values[field.name] = Number(input.value);
        else if (field.type === "strings") values[field.name] = input.value.split(/\r?\n/).map(v => v.trim()).filter(Boolean);
        else values[field.name] = input.value;
      }
      button.disabled = true;
      try {
        const result = await api("/api/components", { method: "PATCH", body: JSON.stringify({ id: component.id, values, revision: component.revision || "" }) });
        const saved = result.components.find(item => item.id === component.id);
        if (saved) component.revision = saved.revision;
        report(saved?.pending_restart ? "已保存，正在重新配置此服务。" : "配置已保存并应用。");
        window.dispatchEvent(new CustomEvent("configuration-changed", { detail: { id: component.id } }));
        if (saved) status.textContent = `${saved.enabled === false ? "已停用" : "已启用"} · ${saved.state}${saved.pending_restart ? " · 等待重启生效" : ""}${saved.error ? ` · ${saved.error}` : ""}`;
        for (const { field, input } of fields) if (field.secret && input) input.value = "";
      } catch (error) {
        report(error.message);
      } finally {
        button.disabled = false;
      }
    });
    host.append(form);
  }
}


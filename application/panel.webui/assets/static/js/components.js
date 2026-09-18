import { api } from "./api.js";

const host = document.getElementById("component-forms");
const message = document.getElementById("component-message");

function render(data) {
  host.replaceChildren();
  document.getElementById("component-mode").textContent = data.editable
    ? "配置以此处保存的值为准。标注重启的字段将在对应服务重启后生效；未启动的组件也可编辑。"
    : "当前为只读预览。启用组件配置目录后可在此编辑；当前设置仍由配置文件页面管理。";
  for (const component of data.components) {
    const form = document.createElement("form");
    form.className = "config-section";
    const title = document.createElement("h2");
    title.textContent = component.title || component.id;
    form.append(title);
    const status = document.createElement("p");
    status.textContent = `${component.enabled === false ? "已停用" : "已启用"} · ${component.state}${component.pending_restart ? " · 等待重启生效" : ""}${component.error ? ` · ${component.error}` : ""}`;
    form.append(status);
    if (data.can_toggle) {
      const toggle = document.createElement("button");
      toggle.type = "button";
      toggle.className = "btn";
      toggle.textContent = component.enabled === false ? "启用组件" : "停用组件";
      toggle.addEventListener("click", async () => {
        toggle.disabled = true;
        try {
          await api("/api/components", { method: "PATCH", body: JSON.stringify({ id: component.id, enabled: !component.enabled, revision: component.revision || "" }) });
          message.textContent = "启停请求已保存，正在协调依赖；可刷新运行状态查看结果。";
          render(await api("/api/components"));
        } catch (error) {
          message.textContent = error.message;
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
      const label = document.createElement("label");
      label.style.display = "block";
      label.style.margin = "16px 0";
      const caption = document.createElement("span");
      caption.textContent = `${field.title || field.name}${field.restart_required ? "（需要重启）" : ""}`;
      label.append(caption);
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
      for (const { field, input } of fields) {
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
        message.textContent = saved?.pending_restart ? "已保存，等待服务重启后生效。可刷新运行状态查看结果。" : "配置已保存。";
        if (saved) status.textContent = `${saved.enabled === false ? "已停用" : "已启用"} · ${saved.state}${saved.pending_restart ? " · 等待重启生效" : ""}${saved.error ? ` · ${saved.error}` : ""}`;
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

async function refreshHealth() {
  const target = document.getElementById("component-health");
  const button = document.getElementById("refresh-health");
  button.disabled = true;
  try {
    const data = await api("/api/components/health");
    target.replaceChildren();
    for (const runtime of data.hosts) {
      for (const component of runtime.components) {
        const line = document.createElement("p");
        line.textContent = `${runtime.account} / ${component.id}：${component.state}${component.detail ? ` — ${component.detail}` : ""}`;
        target.append(line);
        for (const runnable of component.runnables || []) {
          const detail = document.createElement("p");
          detail.textContent = `${runnable.name}：已执行 ${runnable.runs} 次${runnable.running ? "，运行中" : ""}${runnable.overdue ? "，超过调度周期" : ""}${runnable.last_error ? `，${runnable.last_error}` : ""}`;
          target.append(detail);
        }
      }
      for (const event of runtime.events || []) {
        const line = document.createElement("p");
        line.textContent = `${new Date(event.at).toLocaleString()} / ${event.component} / ${event.operation}：${event.message}`;
        target.append(line);
      }
    }
    if (!data.hosts.length) target.textContent = "暂无运行中的组件宿主。";
  } catch (error) {
    target.textContent = error.message;
  } finally {
    button.disabled = false;
  }
}

document.getElementById("refresh-health").addEventListener("click", refreshHealth);
refreshHealth();

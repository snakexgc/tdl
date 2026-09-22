import { api } from "./api.js";
import { renderComponentForms } from "./component-form.js";

const host = document.getElementById("component-forms");
const message = document.getElementById("component-message");

async function render(data) { await renderComponentForms(host, message, data); }

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

import { api } from "./api.js";
import { element, button } from "./ui.js";
export function renderDiagnostics(host) {
  const diagnostics = element(
    "details",
    null,
    "settings-block diagnostic-details",
  );
  diagnostics.id = "diagnostics";
  diagnostics.append(element("summary", "组件健康与诊断事件"));
  const output = element("div", null, "diagnostic-content");
  const refresh = button("刷新诊断", async () => {
    refresh.disabled = true;
    output.textContent = "正在读取…";
    try {
      const health = await api("/api/components/health");
      output.replaceChildren();
      for (const runtime of health.hosts || []) {
        output.append(element("h3", `账号：${runtime.account || "默认"}`));
        for (const component of runtime.components || []) {
          const entry = element("details");
          entry.append(
            element("summary", `${component.id} · ${component.state}`),
          );
          if (component.detail) entry.append(element("p", component.detail));
          for (const runnable of component.runnables || [])
            entry.append(
              element(
                "p",
                `${runnable.name}：已运行 ${runnable.runs} 次${runnable.running ? " · 运行中" : ""}${runnable.overdue ? " · 超过调度周期" : ""}${runnable.last_error ? ` · ${runnable.last_error}` : ""}`,
              ),
            );
          output.append(entry);
        }
        for (const event of runtime.events || [])
          output.append(
            element(
              "p",
              `${new Date(event.at).toLocaleString()} · ${event.component} · ${event.operation}：${event.message}`,
            ),
          );
      }
      if (!output.children.length)
        output.textContent = "暂无运行中的组件宿主。";
    } catch (error) {
      output.textContent = `诊断不可用：${error.message}`;
    } finally {
      refresh.disabled = false;
    }
  });
  diagnostics.append(refresh, output);
  diagnostics.addEventListener("toggle", () => {
    if (diagnostics.open && !output.textContent) refresh.click();
  });
  host.append(diagnostics);
}

import { element } from "./ui.js";
import { componentStatus } from "./module-model.js";

const aliases = {
  "host.bot": "console.bot",
  "host.panel": "panel.webui",
  "host.aria2": "downloader.aria2",
  http: "proxy.range",
  watch: "account.telegram",
};
export function renderDiagnostics(
  host,
  {
    hosts = [],
    components = [],
    feature = "",
    component = "",
    query = "",
  } = {},
) {
  const names = new Map(components.map((item) => [item.id, item]));
  const expanded = new Set(
    [...host.querySelectorAll("details[open]")].map((item) => item.id),
  );
  host.replaceChildren();
  for (const [index, runtime] of hosts.entries()) {
    for (const status of runtime.components || []) {
      const id = aliases[status.id] || status.id;
      const info = names.get(id);
      const events = (runtime.events || []).filter(
        (event) => (aliases[event.component] || event.component) === id,
      );
      const search = JSON.stringify([
        info?.title,
        status,
        events,
      ]).toLocaleLowerCase();
      if (
        (component && component !== id) ||
        (feature && info?.feature?.id !== feature) ||
        (query && !search.includes(query.toLocaleLowerCase()))
      )
        continue;
      const card = element("details", null, "log-health-card");
      card.id = `log-health-${index}-${status.id}`;
      card.open = expanded.has(card.id);
      const health = componentStatus({
        ...status,
        error: status.state === "failed" ? status.detail : "",
      });
      card.append(
        element("summary", `${info?.title || status.id} · ${health.text}`),
        element(
          "p",
          `${info?.feature?.title || "系统运行"} · ${status.id}`,
          "subtle",
        ),
      );
      if (status.detail) card.append(element("p", status.detail));
      for (const runnable of status.runnables || [])
        card.append(
          element(
            "p",
            `${runnable.name}：已运行 ${runnable.runs} 次${runnable.running ? " · 运行中" : ""}${runnable.overdue ? " · 超过调度周期" : ""}${runnable.last_error ? ` · ${runnable.last_error}` : ""}`,
          ),
        );
      if (events.length) {
        const history = element("div", null, "log-health-events");
        history.append(element("strong", "诊断事件"));
        for (const event of [...events].reverse())
          history.append(
            element(
              "p",
              `${new Date(event.at).toLocaleString()} · ${event.operation}：${event.message}`,
            ),
          );
        card.append(history);
      }
      host.append(card);
    }
  }
  if (!host.children.length)
    host.append(element("p", "当前筛选下暂无组件健康记录。", "empty"));
}

import { api } from "./api.js";
import { renderComponentForms } from "./component-form.js";

export function mountSettingsTabs(page, isActive) {
  const root = page.section.querySelector("section.view") || page.section;
  const header = root.querySelector(".page-head");
  const bar = document.createElement("div");
  bar.className = "module-tabs";
  bar.setAttribute("role", "tablist");
  bar.setAttribute("aria-label", `${page.title}页面`);
  const info = document.createElement("div");
  const settings = document.createElement("div");
  info.className = settings.className = "module-tab-panel";
  info.id = `${page.view}-information`;
  settings.id = `${page.view}-settings`;
  info.setAttribute("role", "tabpanel");
  settings.setAttribute("role", "tabpanel");
  for (const node of [...root.childNodes]) if (node !== header) info.append(node);
  root.append(bar, info, settings);
  settings.hidden = true;
  let loaded = false, loading;
  const buttons = ["运行信息", "配置"].map((label, index) => {
    const button = document.createElement("button");
    button.type = "button";
    button.setAttribute("role", "tab");
    button.id = `${page.view}-tab-${index}`;
    button.setAttribute("aria-controls", index ? settings.id : info.id);
    button.setAttribute("aria-selected", String(index === 0));
    button.tabIndex = index ? -1 : 0;
    button.textContent = label;
    button.addEventListener("click", () => select(index));
    bar.append(button);
    return button;
  });
  info.setAttribute("aria-labelledby", buttons[0].id);
  settings.setAttribute("aria-labelledby", buttons[1].id);
  bar.addEventListener("keydown", event => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const index = event.key === "Home" ? 0 : event.key === "End" ? 1 : settings.hidden ? 1 : 0;
    buttons[index].focus();
    select(index);
  });
  async function load() {
    if (loaded) return;
    if (loading) return loading;
    settings.textContent = "正在加载配置…";
    loading = (async () => {
      try {
        const data = await api("/api/components");
        const forms = document.createElement("div");
        const reload = document.createElement("button");
        reload.type = "button";
        reload.className = "btn";
        reload.textContent = "重新加载（放弃未保存更改）";
        reload.addEventListener("click", () => { loaded = false; void load(); });
        settings.replaceChildren(reload, forms);
        await renderComponentForms(forms, null, data, page.settings);
        if (!data.editable) {
          const note = document.createElement("p");
          note.className = "notice warn";
          note.textContent = "当前配置服务不可用，请检查服务运行状态。";
          settings.prepend(note);
        }
        loaded = true;
      } catch (error) { settings.textContent = error.message; }
      finally { loading = null; }
    })();
    return loading;
  }
  async function select(index) {
    if (!isActive()) return;
    buttons.forEach((button, i) => {
      button.setAttribute("aria-selected", String(i === index));
      button.tabIndex = i === index ? 0 : -1;
    });
    info.hidden = index === 1;
    settings.hidden = index === 0;
    header?.querySelectorAll("button, .actions").forEach(action => { action.hidden = index === 1; });
    if (index === 1) { page.hooks?.stop?.(); await load(); }
    else await page.hooks?.load?.();
  }
  root.addEventListener("open-settings", () => select(1));
  return { load, settingsActive: () => !settings.hidden };
}

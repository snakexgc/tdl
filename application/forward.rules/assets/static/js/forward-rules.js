import { api } from "./api.js";

const kinds = { group: "群组", channel: "频道", user: "用户", bot: "机器人", self: "收藏夹" };
// getRandomValues also works on a LAN HTTP origin; randomUUID requires HTTPS.
function ruleID() {
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), byte => byte.toString(16).padStart(2, "0")).join("");
}
let catalog;
function loadCatalog() {
  catalog ||= api("/api/dialogs").then(data => data.items || []).catch(error => { catalog = null; throw error; });
  return catalog;
}

function node(tag, text, className) {
  const element = document.createElement(tag);
  if (text) element.textContent = text;
  if (className) element.className = className;
  return element;
}
function button(text, action) {
  const element = node("button", text, "btn secondary");
  element.type = "button";
  element.addEventListener("click", action);
  return element;
}

export async function createEditor(value, { editable }) {
  if (!document.querySelector('link[data-forward-rules-style]')) {
    const style = document.createElement("link");
    style.rel = "stylesheet";
    style.href = "/static/css/forward-rules.css";
    style.dataset.forwardRulesStyle = "true";
    document.head.append(style);
  }
  const root = node("div", "", "forward-rules");
  const help = node("p", "每条规则可选择多个来源和多个目标。所有匹配规则都会执行；相同目标只发送一次，采用靠前规则的模式。匹配规则的来源不再走旧版默认目标。保存后对新消息立即生效，请同时启用转发触发组件。", "rule-help");
  const status = node("p", "", "notice");
  status.setAttribute("role", "status");
  const cards = node("div", "", "rule-cards");
  let rules = structuredClone(Array.isArray(value) ? value : []), dialogs = [], loaded = false;
  const refresh = button("刷新聊天列表", async () => { catalog = null; await refreshDialogs(); });
  const add = button("＋ 添加规则", () => {
    rules.push({ id: ruleID(), name: `转发规则 ${rules.length + 1}`, enabled: true, sources: [], targets: [], mode: "default", silent: false });
    render();
    cards.lastElementChild?.querySelector("input")?.focus();
  });
  add.disabled = !editable;
  root.append(help, refresh, add, status, cards);

  function picker(title, selected, allowSelf, change) {
    const box = node("fieldset", "", "chat-picker");
    box.disabled = !editable;
    box.append(node("legend", title));
    const search = node("input");
    search.type = "search";
    search.placeholder = "搜索名称、@用户名或 ID";
    search.setAttribute("aria-label", `${title}搜索`);
    const filter = node("select");
    filter.setAttribute("aria-label", `${title}类型`);
    for (const [key, label] of Object.entries({ all: "全部类型", ...kinds })) {
      if (key === "self" && !allowSelf) continue;
      const option = node("option", label); option.value = key; filter.append(option);
    }
    const chips = node("div", "", "selected-chats");
    const list = node("div", "", "chat-results");
    const choices = () => allowSelf ? [{ ref: "self", title: "收藏夹", kind: "self" }, ...dialogs] : dialogs;
    const update = () => {
      chips.replaceChildren();
      for (const ref of selected) {
        const item = choices().find(item => item.ref === ref);
        const chip = button(`${item?.title || ref} ×`, () => { selected.splice(selected.indexOf(ref), 1); change(); update(); });
        chip.setAttribute("aria-label", `移除 ${item?.title || ref}`);
        chips.append(chip);
      }
      list.replaceChildren();
      const query = search.value.toLocaleLowerCase();
      const matches = choices().filter(item => (filter.value === "all" || item.kind === filter.value) && `${item.title} @${item.username || ""} ${item.ref}`.toLocaleLowerCase().includes(query));
      // Keep DOM work bounded even for accounts with thousands of dialogs.
      for (const item of matches.slice(0, 100)) {
        const label = node("label", "", "chat-choice");
        const check = node("input"); check.type = "checkbox"; check.checked = selected.includes(item.ref);
        check.addEventListener("change", () => {
          if (check.checked) selected.push(item.ref); else selected.splice(selected.indexOf(item.ref), 1);
          change(); update();
        });
        label.append(check, node("span", `${item.title || item.ref} ${item.username ? `@${item.username}` : ""}`), node("small", kinds[item.kind] || item.kind));
        list.append(label);
      }
      if (matches.length > 100) list.append(node("p", `显示前 100 项，共 ${matches.length} 项；输入名称继续筛选。`));
      if (!matches.length) list.append(node("p", loaded ? "没有匹配的聊天。" : "聊天列表尚未加载；已保存的选择会保留。"));
    };
    search.addEventListener("input", update); filter.addEventListener("change", update);
    box.append(search, filter, chips, list); update(); return box;
  }

  function render() {
    cards.replaceChildren();
    if (!rules.length) cards.append(node("p", "暂无规则。添加一条规则，选择来源和目标即可。", "rule-empty"));
    rules.forEach((rule, index) => {
      const card = node("section", "", "forward-rule-card");
      const heading = node("div", "", "rule-heading");
      const name = node("input"); name.value = rule.name; name.maxLength = 100; name.setAttribute("aria-label", "规则名称"); name.disabled = !editable;
      name.addEventListener("input", () => { rule.name = name.value; });
      const enabled = node("input"); enabled.type = "checkbox"; enabled.checked = rule.enabled; enabled.disabled = !editable;
      enabled.addEventListener("change", () => { rule.enabled = enabled.checked; });
      const enableLabel = node("label", "启用 "); enableLabel.append(enabled);
      const remove = button("删除", () => { rules.splice(index, 1); render(); }); remove.disabled = !editable;
      const copy = button("复制", () => { rules.splice(index + 1, 0, { ...structuredClone(rule), id: ruleID(), name: `${Array.from(rule.name).slice(0, 97).join("")} 副本` }); render(); }); copy.disabled = !editable;
      const up = button("上移", () => { [rules[index - 1], rules[index]] = [rules[index], rules[index - 1]]; render(); }); up.disabled = !editable || index === 0;
      const down = button("下移", () => { [rules[index + 1], rules[index]] = [rules[index], rules[index + 1]]; render(); }); down.disabled = !editable || index === rules.length - 1;
      heading.append(name, enableLabel, up, down, copy, remove);
      const summary = node("p", "", "rule-summary");
      const summarize = () => { summary.textContent = `${rule.sources.length} 个来源 → ${rule.targets.length} 个目标`; };
      const columns = node("div", "", "rule-columns");
      columns.append(picker("来源", rule.sources, false, summarize), picker("目标", rule.targets, true, summarize));
      const mode = node("select"); mode.setAttribute("aria-label", "转发模式"); mode.disabled = !editable;
      for (const [v, text] of [["default", "官方转发优先"], ["clone", "复制发送"]]) { const option = node("option", text); option.value = v; mode.append(option); }
      mode.value = rule.mode; mode.addEventListener("change", () => { rule.mode = mode.value; });
      const silent = node("input"); silent.type = "checkbox"; silent.checked = rule.silent; silent.disabled = !editable;
      silent.addEventListener("change", () => { rule.silent = silent.checked; });
      const silentLabel = node("label", " 静默发送 "); silentLabel.append(silent);
      card.append(heading, summary, columns, mode, silentLabel); summarize(); cards.append(card);
    });
  }
  async function refreshDialogs() {
    refresh.disabled = true; status.textContent = "正在读取账号聊天列表（含归档）…";
    try { dialogs = await loadCatalog(); loaded = true; status.textContent = `已加载 ${dialogs.length} 个聊天。`; render(); }
    catch (error) { status.textContent = `聊天列表加载失败：${error.message}。可重试；已保存的规则不受影响。`; }
    finally { refresh.disabled = false; }
  }
  render();
  void refreshDialogs();
  return { element: root, value: () => structuredClone(rules) };
}

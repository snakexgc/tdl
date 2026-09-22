export function element(tag, text, className) {
  const node = document.createElement(tag);
  if (text != null) node.textContent = text;
  if (className) node.className = className;
  return node;
}
export function button(text, action, className = "btn secondary") {
  const node = element("button", text, className);
  node.type = "button";
  node.addEventListener("click", action);
  return node;
}
export async function fragment(host, name) {
  const response = await fetch(`/views/${name}.html`, {
    credentials: "same-origin",
  });
  if (response.status === 401) {
    window.location.href = "/login";
    throw new Error("登录已过期。");
  }
  if (!response.ok) throw new Error("页面内容加载失败，请重试。");
  host.innerHTML = await response.text();
  host
    .querySelectorAll(".view")
    .forEach((node) => node.classList.add("active"));
}
export function tabKeyboard(host) {
  host.setAttribute("role", "tablist");
  host.addEventListener("keydown", (event) => {
    if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key)) return;
    const tabs = [...host.querySelectorAll('[role="tab"]')];
    const index = tabs.indexOf(document.activeElement);
    if (index < 0) return;
    event.preventDefault();
    const next =
      event.key === "Home"
        ? 0
        : event.key === "End"
          ? tabs.length - 1
          : (index + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
            tabs.length;
    tabs[next].focus();
    tabs[next].click();
  });
}
export function activateTabs(host, value, attribute = "tab") {
  host.querySelectorAll('[role="tab"]').forEach((node) => {
    const active = node.dataset[attribute] === value;
    node.classList.toggle("active", active);
    node.setAttribute("aria-selected", String(active));
    node.tabIndex = active ? 0 : -1;
  });
}
let drawer;
export function closeDrawer() {
  drawer?.close();
}
export function openDrawer(title, content, onClose) {
  closeDrawer();
  const origin = document.activeElement;
  const dialog = element("dialog", null, "detail-drawer");
  drawer = dialog;
  const header = element("header", null, "drawer-head"),
    heading = element("h2", title);
  heading.id = "drawer-heading";
  dialog.setAttribute("aria-labelledby", heading.id);
  const close = button("关闭", () => dialog.close());
  close.setAttribute("aria-label", `关闭${title}`);
  header.append(heading, close);
  const body = element("div", null, "drawer-body");
  body.append(content);
  dialog.append(header, body);
  document.body.append(dialog);
  dialog.addEventListener("keydown", (event) => {
    if (event.key !== "Tab") return;
    const nodes = [
      ...dialog.querySelectorAll(
        'a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]',
      ),
    ].filter((node) => node.getClientRects().length);
    const first = nodes[0],
      last = nodes.at(-1);
    if (!first) {
      event.preventDefault();
      return;
    }
    if (
      event.shiftKey &&
      (document.activeElement === first ||
        !dialog.contains(document.activeElement))
    ) {
      event.preventDefault();
      last.focus();
    } else if (
      !event.shiftKey &&
      (document.activeElement === last ||
        !dialog.contains(document.activeElement))
    ) {
      event.preventDefault();
      first.focus();
    }
  });
  dialog.addEventListener("click", (event) => {
    if (
      event.target === dialog &&
      event.clientX < dialog.getBoundingClientRect().left
    )
      dialog.close();
  });
  dialog.addEventListener(
    "close",
    () => {
      onClose?.();
      dialog.remove();
      if (drawer === dialog) drawer = null;
      if (origin?.isConnected) origin.focus();
      if (!drawer) document.body.classList.remove("drawer-open");
    },
    { once: true },
  );
  dialog.showModal();
  document.body.classList.add("drawer-open");
  close.focus();
  return dialog;
}
window.addEventListener("page-changing", closeDrawer);

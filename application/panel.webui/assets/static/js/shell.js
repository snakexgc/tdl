// Responsive navigation belongs to the shell; page declarations remain dynamic.
export function initShell() {
  const sidebar = document.getElementById("app-sidebar");
  const toggle = document.getElementById("menu-toggle");
  const backdrop = document.getElementById("sidebar-backdrop");
  const close = document.getElementById("sidebar-close");
  const content = document.querySelector("main.main");
  const background = document.querySelectorAll(".skip-link, .topbar, main.main");
  const mobile = window.matchMedia("(max-width: 900px)");
  let open = false;

  function focusNavigation() {
    const link = sidebar.querySelector(".nav-item.active") || sidebar.querySelector(".nav-item");
    (link || close).focus();
  }

  function setOpen(value, restoreFocus = false) {
    open = mobile.matches && value;
    document.body.classList.toggle("navigation-open", open);
    toggle.setAttribute("aria-expanded", String(open));
    toggle.setAttribute("aria-label", open ? "关闭导航" : "打开导航");
    sidebar.inert = mobile.matches && !open;
    sidebar.role = open ? "dialog" : "complementary";
    sidebar.ariaModal = open ? "true" : null;
    for (const element of background) element.inert = open;
    backdrop.hidden = !open;
    if (open) focusNavigation();
    else if (restoreFocus) toggle.focus();
  }

  toggle.addEventListener("click", () => setOpen(!open));
  backdrop.addEventListener("click", () => setOpen(false, true));
  close.addEventListener("click", () => setOpen(false, true));
  document.querySelector(".skip-link").addEventListener("click", event => {
    event.preventDefault();
    content.focus();
  });
  document.addEventListener("keydown", event => {
    if (!open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false, true);
    }
    if (event.key === "Tab") {
      const items = sidebar.querySelectorAll("a[href], button:not(:disabled)");
      const first = items[0], last = items[items.length - 1];
      const boundary = event.shiftKey ? first : last;
      if (document.activeElement === boundary || !sidebar.contains(document.activeElement)) {
        event.preventDefault();
        (event.shiftKey ? last : first)?.focus();
      }
    }
  });
  mobile.addEventListener("change", () => {
    const active = document.activeElement;
    setOpen(false);
    if (mobile.matches && sidebar.contains(active)) toggle.focus();
    else if (!mobile.matches && (active === toggle || active === close)) {
      (sidebar.querySelector(".nav-item.active") || document.getElementById("logout")).focus();
    }
  });
  window.addEventListener("page-selected", () => {
    if (open) setOpen(false);
    content.focus();
  });
  setOpen(false);
}

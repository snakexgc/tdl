// Responsive navigation belongs to the shell; page declarations remain dynamic.
export function initShell() {
  const sidebar = document.getElementById("app-sidebar");
  const toggle = document.getElementById("menu-toggle");
  const backdrop = document.getElementById("sidebar-backdrop");
  const close = document.getElementById("sidebar-close");
  const content = document.querySelector("main.main");
  const mobile = window.matchMedia("(max-width: 900px)");
  let open = false;

  function setOpen(value, restoreFocus = false) {
    open = mobile.matches && value;
    document.body.classList.toggle("navigation-open", open);
    toggle.setAttribute("aria-expanded", String(open));
    toggle.setAttribute("aria-label", open ? "关闭导航" : "打开导航");
    sidebar.inert = mobile.matches && !open;
    sidebar.role = open ? "dialog" : "complementary";
    sidebar.ariaModal = open ? "true" : null;
    content.inert = open;
    backdrop.hidden = !open;
    if (open) sidebar.querySelector(".nav-item.active, .nav-item")?.focus();
    else if (restoreFocus) toggle.focus();
  }

  toggle.addEventListener("click", () => setOpen(!open));
  backdrop.addEventListener("click", () => setOpen(false, true));
  close.addEventListener("click", () => setOpen(false, true));
  sidebar.addEventListener("click", event => {
    if (event.target.closest("a.nav-item") && open) setOpen(false, true);
  });
  document.addEventListener("keydown", event => {
    if (!open) return;
    if (event.key === "Escape") {
      event.preventDefault();
      setOpen(false, true);
    }
    if (event.key === "Tab") {
      const items = [...sidebar.querySelectorAll("a[href], button:not(:disabled)")];
      const index = items.indexOf(document.activeElement);
      event.preventDefault();
      items[(index + (event.shiftKey ? -1 : 1) + items.length) % items.length]?.focus();
    }
  });
  mobile.addEventListener("change", () => setOpen(false));
  window.addEventListener("popstate", () => setOpen(false));
  setOpen(false);
}

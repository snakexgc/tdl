document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById("webui-login-form");
  const submit = document.getElementById("webui-login-submit");
  const status = document.getElementById("webui-login-status");
  const usernameField = document.getElementById("webui-username");
  const passwordField = document.getElementById("webui-password");
  const reveal = document.getElementById("webui-password-toggle");

  if (window.matchMedia("(hover: hover) and (pointer: fine)").matches && document.activeElement === document.body) {
    usernameField.focus();
  }
  reveal.hidden = false;
  reveal.addEventListener("click", () => {
    const show = passwordField.type === "password";
    passwordField.type = show ? "text" : "password";
    reveal.textContent = show ? "隐藏" : "显示";
    reveal.setAttribute("aria-label", show ? "隐藏密码" : "显示密码");
  });

  fetch("/api/auth/session", { credentials: "same-origin" })
    .then((response) => response.ok ? response.json() : null)
    .then((data) => {
      if (data && data.authenticated) {
        window.location.replace("/");
      }
    })
    .catch(() => {});

  form.noValidate = true;
  form.addEventListener("input", () => {
    if (form.querySelector('[aria-invalid="true"]')) clearFieldError();
  });
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (submit.getAttribute("aria-disabled") === "true") return;
    clearFieldError();
    const username = usernameField.value.trim();
    const password = passwordField.value;
    if (!username || !password) {
      const missingField = username ? passwordField : usernameField;
      setStatus(username ? "请输入密码。" : "请输入用户名。", "error");
      markInvalid(missingField);
      missingField.focus();
      return;
    }

    submit.setAttribute("aria-disabled", "true");
    setStatus("正在登录…");
    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username, password }),
      });
      if (response.status === 401) {
        setStatus("用户名或密码有误，请检查后重试。", "error");
        if (usernameField.value.trim() === username && passwordField.value === password) {
          markInvalid(usernameField);
          markInvalid(passwordField);
          passwordField.focus();
          passwordField.select();
        }
        return;
      }
      if (response.status === 429) {
        const seconds = Number(response.headers.get("Retry-After"));
        const retryAt = new Date(Date.now() + seconds * 1000);
        const hint = Number.isFinite(seconds) && seconds > 0
          ? `请在 ${retryAt.toLocaleTimeString("zh-CN", { hour12: false })} 后重试。`
          : "请稍后重试。";
        setStatus(`登录尝试过多，${hint}`, "error");
        return;
      }
      if (!response.ok) {
        setStatus("登录服务暂时不可用，请稍后重试。", "error");
        return;
      }
      const data = await response.json().catch(() => null);
      if (!data || data.ok !== true) {
        setStatus("未能确认登录结果，请重试。", "error");
        return;
      }
      try {
        window.sessionStorage.setItem(
          "tdl-default-credentials-warning",
          data.using_default_credentials ? "1" : "0",
        );
      } catch {
        // Browser storage restrictions must not prevent a successful login.
      }
      setStatus("登录成功，正在进入面板…", "success");
      window.location.replace("/");
    } catch {
      setStatus("未能连接到面板，请检查连接后重试。", "error");
    } finally {
      if (status.dataset.kind !== "success") submit.removeAttribute("aria-disabled");
    }
  });

  function markInvalid(field) {
    field.setAttribute("aria-invalid", "true");
    field.setAttribute("aria-describedby", status.id);
  }

  function clearFieldError() {
    for (const field of [usernameField, passwordField]) {
      field.removeAttribute("aria-invalid");
      field.removeAttribute("aria-describedby");
    }
    setStatus("");
  }

  function setStatus(message, kind = "") {
    status.dataset.kind = kind;
    status.textContent = message;
  }
});

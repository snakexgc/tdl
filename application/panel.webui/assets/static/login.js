document.addEventListener("DOMContentLoaded", () => {
  const form = document.getElementById("webui-login-form");
  const submit = document.getElementById("webui-login-submit");
  const status = document.getElementById("webui-login-status");

  fetch("/api/auth/session", { credentials: "same-origin" })
    .then((response) => response.ok ? response.json() : null)
    .then((data) => {
      if (data && data.authenticated) {
        window.location.replace("/");
      }
    })
    .catch(() => {});

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const username = document.getElementById("webui-username").value.trim();
    const password = document.getElementById("webui-password").value;
    if (!username || !password) {
      setStatus("请输入用户名和密码。", "error");
      return;
    }

    submit.disabled = true;
    setStatus("正在登录...", "");
    try {
      const response = await fetch("/api/auth/login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        credentials: "same-origin",
        body: JSON.stringify({ username, password }),
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) {
        throw new Error(data.error || "登录失败。");
      }
      setStatus("登录成功，正在进入面板...", "success");
      window.location.replace("/");
    } catch (error) {
      setStatus(error.message, "error");
      submit.disabled = false;
    }
  });

  function setStatus(message, kind) {
    status.className = `notice ${kind}`.trim();
    status.textContent = message;
  }
});

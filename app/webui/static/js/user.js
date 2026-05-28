// User view: current Telegram account info, account switching, and the login flow.
import { state } from "./state.js";
import { api } from "./api.js";
import { escapeHTML, escapeAttr, infoItem } from "./utils.js";
import { loadStatus } from "./status.js";

export function initUser() {
  document.getElementById("refresh-user").addEventListener("click", loadUser);
  document.getElementById("switch-user").addEventListener("click", switchUser);
  document.getElementById("delete-user").addEventListener("click", deleteUser);
  document.getElementById("switch-namespace").addEventListener("change", () => updateUserActionButtons());

  bindUserTabs();
  bindLoginActions();
}

function bindUserTabs() {
  document.querySelectorAll("[data-user-tab]").forEach((button) => {
    button.addEventListener("click", () => {
      const tab = button.dataset.userTab;
      document.querySelectorAll("[data-user-tab]").forEach((item) => item.classList.toggle("active", item === button));
      document.querySelectorAll(".user-tab").forEach((item) => item.classList.toggle("active", item.id === `user-tab-${tab}`));
      if (tab === "current") loadUser();
      if (tab === "login") loadLoginStatus();
    });
  });
}

function bindLoginActions() {
  document.getElementById("start-phone-login").addEventListener("click", startPhoneLogin);
  document.getElementById("submit-login-code").addEventListener("click", submitLoginCode);
  document.getElementById("submit-login-password").addEventListener("click", submitLoginPassword);
  document.getElementById("cancel-login").addEventListener("click", cancelLogin);
  document.getElementById("restart-login-flow").addEventListener("click", resetLoginFlow);
  document.getElementById("finish-login-flow").addEventListener("click", finishLoginFlow);

  [
    ["login-namespace", startSelectedLogin],
    ["login-phone", startPhoneLogin],
    ["login-code", submitLoginCode],
    ["login-password", submitLoginPassword],
  ].forEach(([id, submit]) => {
    const input = document.getElementById(id);
    if (!input) return;
    input.addEventListener("keydown", (event) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      submit();
    });
  });

  updateLoginMethodUI();
}

export async function loadUser() {
  const target = document.getElementById("user-info");
  target.innerHTML = `<div class="info-item info-item-wide"><div class="info-value">正在检查...</div></div>`;
  try {
    const data = await api("/api/user");
    const user = data.user || {};
    target.innerHTML = renderUserInfoItems(data, user);
    state.userSessions = data.sessions || [];
    state.currentNamespace = data.namespace || "";
    renderUserSwitch(data.namespace || "", state.userSessions, data.sessions_error || "");
    setNamespaceInputs(data.namespace || "");
    if (data.valid) {
      checkAccountSpam();
    }
  } catch (error) {
    target.innerHTML = infoItem("检查失败", error.message);
  }
}

function renderUserInfoItems(data, user) {
  const items = [];

  // 1. Login status — colored row
  items.push(infoItemStatus("登录状态", data.valid ? "已授权" : "未登录",
    data.valid ? "info-item-status-ok" : "info-item-status-danger"));

  if (!data.valid) {
    const errMsg = (data.status && data.status !== "authorized") ? data.status : "无有效登录会话";
    items.push(infoItemWide("状态详情", escapeHTML(errMsg)));
    return items.join("");
  }

  // 2. Account spam status — colored row, updated async by checkAccountSpam
  items.push(infoItemStatusDyn("账号状态", "检查中...", "info-item-status-muted", "user-spam-row", "user-spam-status"));

  // 3. Account characteristics — colored row, plain text labels
  const charLabels = [];
  if (user.premium)    charLabels.push("Premium 会员");
  if (user.verified)   charLabels.push("官方认证");
  if (user.bot)        charLabels.push("机器人账号");
  if (user.restricted) charLabels.push("账号受限");
  if (!charLabels.length) charLabels.push("普通账号");
  let charStatus = "info-item-status-muted";
  if (user.restricted)       charStatus = "info-item-status-danger";
  else if (user.verified)    charStatus = "info-item-status-info";
  else if (user.premium)     charStatus = "info-item-status-warning";
  items.push(infoItemStatus("账号特性", charLabels.join(" · "), charStatus));

  // 4–7. Plain info fields
  items.push(infoItemWide("姓名", escapeHTML(user.name || "-")));
  items.push(infoItemWide("用户名", user.username ? escapeHTML(`@${user.username}`) : "未设置"));
  items.push(infoItemWide("用户 ID", escapeHTML(user.id ? String(user.id) : "-")));
  items.push(infoItemWide("手机号", escapeHTML(user.phone || "-")));

  return items.join("");
}

function infoItemStatus(label, text, statusClass) {
  return `
    <div class="info-item info-item-wide ${statusClass}">
      <div class="info-label">${escapeHTML(label)}</div>
      <div class="info-value">${escapeHTML(text)}</div>
    </div>
  `;
}

function infoItemStatusDyn(label, text, statusClass, rowId, valueId) {
  return `
    <div class="info-item info-item-wide ${statusClass}" id="${escapeAttr(rowId)}">
      <div class="info-label">${escapeHTML(label)}</div>
      <div class="info-value" id="${escapeAttr(valueId)}">${escapeHTML(text)}</div>
    </div>
  `;
}

function infoItemWide(label, valueHTML) {
  return `
    <div class="info-item info-item-wide">
      <div class="info-label">${escapeHTML(label)}</div>
      <div class="info-value">${valueHTML}</div>
    </div>
  `;
}

async function checkAccountSpam() {
  try {
    const data = await api("/api/user/spam-check", { method: "POST", body: "{}" });
    const row = document.getElementById("user-spam-row");
    const el = document.getElementById("user-spam-status");
    if (!row || !el) return;
    if (data.clean) {
      row.className = "info-item info-item-wide info-item-status-ok";
      el.textContent = "正常";
    } else {
      row.className = "info-item info-item-wide info-item-status-danger";
      el.textContent = "账号异常";
    }
  } catch (error) {
    const row = document.getElementById("user-spam-row");
    const el = document.getElementById("user-spam-status");
    if (!row || !el) return;
    row.className = "info-item info-item-wide info-item-status-muted";
    el.textContent = "检查失败";
  }
}

function setNamespaceInputs(namespace) {
  const loginInput = document.getElementById("login-namespace");
  if (loginInput && !loginInput.value) loginInput.value = namespace || "";
}

function renderUserSwitch(currentNamespace, sessions, error = "") {
  const select = document.getElementById("switch-namespace");
  const button = document.getElementById("switch-user");
  const deleteButton = document.getElementById("delete-user");
  if (!select || !button || !deleteButton) return;

  const usable = Array.isArray(sessions) ? sessions.filter((item) => item && item.namespace) : [];
  if (!usable.length) {
    select.innerHTML = `<option value="">${escapeHTML(error || "未发现已登录用户")}</option>`;
    select.disabled = true;
    button.disabled = true;
    deleteButton.disabled = true;
    deleteButton.title = "";
    return;
  }

  select.disabled = false;
  select.innerHTML = usable.map((item) => {
    const label = item.current || item.namespace === currentNamespace ? `${item.namespace}（当前）` : item.namespace;
    return `<option value="${escapeAttr(item.namespace)}">${escapeHTML(label)}</option>`;
  }).join("");
  if (usable.some((item) => item.namespace === currentNamespace)) {
    select.value = currentNamespace;
  }
  updateUserActionButtons(currentNamespace);
}

function updateUserActionButtons(currentNamespace = state.currentNamespace) {
  const select = document.getElementById("switch-namespace");
  const switchButton = document.getElementById("switch-user");
  const deleteButton = document.getElementById("delete-user");
  if (!select || !switchButton || !deleteButton) return;

  const namespace = (select.value || "").trim();
  const hasNamespace = Boolean(namespace);
  switchButton.disabled = !hasNamespace;
  deleteButton.disabled = !hasNamespace || namespace === currentNamespace;
  deleteButton.title = namespace === currentNamespace ? "请先切换到其他用户后再删除当前用户" : "";
}

export async function loadLoginStatus() {
  try {
    const data = await api("/api/login/status");
    renderLoginStatus(data);
    if (data.active) {
      startLoginPolling();
    } else {
      stopLoginPolling();
    }
  } catch (error) {
    renderLoginError(error.message);
  }
}

async function startPhoneLogin() {
  state.loginMethod = "phone";
  updateLoginMethodUI();
  const phone = document.getElementById("login-phone").value.trim();
  if (!phone) {
    renderLoginError("请输入手机号。");
    return;
  }
  const namespace = readNamespaceInput("login-namespace", renderLoginError);
  if (!namespace) return;
  await loginRequest("/api/login/phone/start", { phone, namespace });
}

async function submitLoginCode() {
  const input = document.getElementById("login-code");
  const code = input.value.trim();
  if (!code) {
    renderLoginError("请输入 Telegram 收到的原始验证码。");
    return;
  }
  const data = await loginRequest("/api/login/code", { code });
  if (data) input.value = "";
}

async function submitLoginPassword() {
  const input = document.getElementById("login-password");
  const password = input.value;
  if (!password) {
    renderLoginError("请输入 Telegram 2FA 密码。");
    return;
  }
  const data = await loginRequest("/api/login/password", { password });
  if (data) input.value = "";
}

function startSelectedLogin() {
  startPhoneLogin();
}

async function cancelLogin() {
  try {
    await api("/api/login/cancel", { method: "POST", body: "{}" });
    stopLoginPolling();
    await loadLoginStatus();
  } catch (error) {
    renderLoginError(error.message);
  }
}

async function loginRequest(path, body) {
  setLoginStatus("正在处理登录请求...");
  try {
    const data = await api(path, {
      method: "POST",
      body: JSON.stringify(body || {}),
    });
    renderLoginStatus(data);
    if (data.active) {
      startLoginPolling();
    } else {
      stopLoginPolling();
    }
    return data;
  } catch (error) {
    renderLoginError(error.message);
    return null;
  }
}

function startLoginPolling() {
  if (state.loginPoll) return;
  state.loginPoll = window.setInterval(async () => {
    try {
      const data = await api("/api/login/status");
      renderLoginStatus(data);
      if (!data.active) {
        stopLoginPolling();
        if (data.stage === "done") {
          loadUser();
          loadStatus();
        }
      }
    } catch (error) {
      renderLoginError(error.message);
      stopLoginPolling();
    }
  }, 2000);
}

function stopLoginPolling() {
  if (!state.loginPoll) return;
  window.clearInterval(state.loginPoll);
  state.loginPoll = null;
}

function renderLoginStatus(data) {
  data = data || {};
  state.lastLoginData = data;
  state.loginMethod = "phone";

  updateLoginMethodUI(data);
  renderLoginMeta(data);
  renderLoginSteps(data);
  renderLoginPanel(data);
  renderLoginResult(data);
  updateLoginActions(data);
  setLoginStatus(loginStatusMessage(data), loginStatusKind(data));
}

function updateLoginMethodUI(data = {}) {
  const phoneStart = document.getElementById("login-phone-start");
  if (phoneStart) phoneStart.hidden = false;

  const title = document.getElementById("login-method-title");
  const copy = document.getElementById("login-method-copy");
  if (title) title.textContent = "验证码登录";
  if (copy) copy.textContent = "输入完整手机号后，继续填写 Telegram 收到的验证码。";
}

function renderLoginMeta(data) {
  const meta = document.getElementById("login-meta");
  if (!meta) return;
  const items = [];
  if (data.kind) items.push(["方式", "验证码登录"]);
  if (data.namespace) items.push(["用户", data.namespace]);
  if (data.phone) items.push(["手机号", data.phone]);
  if (data.user) items.push(["Telegram", loginUserLabel(data.user)]);
  meta.innerHTML = items.map(([label, value]) => `
    <span><strong>${escapeHTML(label)}</strong>${escapeHTML(String(value || "-"))}</span>
  `).join("");
}

function renderLoginSteps(data) {
  const order = ["method", "auth", "password", "finish"];
  const current = currentLoginStep(data);
  const currentIndex = order.indexOf(current);
  document.querySelectorAll("[data-login-step]").forEach((item) => {
    const step = item.dataset.loginStep;
    const index = order.indexOf(step);
    const isCurrent = step === current;
    item.classList.toggle("is-current", isCurrent);
    item.classList.toggle("is-complete", index >= 0 && index < currentIndex);
    item.classList.toggle("is-error", step === "finish" && data.stage === "failed");
    if (isCurrent) {
      item.setAttribute("aria-current", "step");
    } else {
      item.removeAttribute("aria-current");
    }
  });
}

function currentLoginStep(data = {}) {
  if (data.stage === "done" || data.stage === "failed") return "finish";
  if (data.stage === "password") return "password";
  if (data.active && data.kind === "phone") return "auth";
  return "method";
}

function renderLoginPanel(data) {
  let panel = "login-step-method";
  if (data.stage === "done" || data.stage === "failed") {
    panel = "login-step-result";
  } else if (data.stage === "password") {
    panel = "login-step-password";
  } else if (data.active && data.kind === "phone") {
    panel = "login-step-code";
  }
  if (showLoginPanel(panel)) {
    focusLoginPanel(panel);
  }
}

function showLoginPanel(panelID) {
  if (state.loginPanel === panelID) return false;
  state.loginPanel = panelID;
  document.querySelectorAll(".login-step-panel").forEach((panel) => {
    const active = panel.id === panelID;
    panel.hidden = !active;
    panel.classList.remove("animate-in");
    if (active) {
      window.requestAnimationFrame(() => panel.classList.add("animate-in"));
    }
  });
  return true;
}

function focusLoginPanel(panelID) {
  const focusTargets = {
    "login-step-method": "login-phone",
    "login-step-code": "login-code",
    "login-step-password": "login-password",
  };
  const target = document.getElementById(focusTargets[panelID]);
  if (!target) return;
  window.setTimeout(() => target.focus(), 120);
}

function renderLoginResult(data) {
  if (data.stage !== "done" && data.stage !== "failed") return;
  const failed = data.stage === "failed";
  const mark = document.getElementById("login-result-mark");
  const title = document.getElementById("login-result-title");
  const copy = document.getElementById("login-result-copy");
  const finish = document.getElementById("finish-login-flow");
  if (mark) {
    mark.textContent = failed ? "!" : "✓";
    mark.classList.toggle("error", failed);
  }
  if (title) title.textContent = failed ? "登录失败" : "登录成功";
  if (copy) {
    copy.textContent = failed
      ? (data.error || data.status || "请检查输入后重新开始。")
      : `${loginUserLabel(data.user)} 已登录。`;
  }
  if (finish) finish.textContent = failed ? "留在此处" : "查看当前用户";
}

function updateLoginActions(data = {}) {
  const cancel = document.getElementById("cancel-login");
  if (cancel) cancel.hidden = !data.active;
}

function loginStatusMessage(data = {}) {
  if (data.stage === "failed") return data.error ? `登录失败：${data.error}` : "登录失败。";
  if (data.active && data.error) return data.error;
  if (data.stage === "done") return "登录成功。";
  if (data.stage === "password") return data.status || "请输入 2FA 密码。";
  if (data.active && data.kind === "phone") return data.status || "请输入 Telegram 收到的验证码。";
  if (data.status && data.status !== "当前没有登录流程。") return data.status;
  return "";
}

function loginStatusKind(data = {}) {
  if (data.error || data.stage === "failed") return "error";
  if (data.stage === "done") return "success";
  return "";
}

function loginUserLabel(user) {
  if (!user) return "-";
  return user.name || user.username || user.id || "-";
}

function resetLoginFlow() {
  state.lastLoginData = {};
  showLoginPanel("login-step-method");
  renderLoginSteps({});
  renderLoginMeta({});
  updateLoginActions({});
  setLoginStatus("");
}

function finishLoginFlow() {
  const done = state.lastLoginData && state.lastLoginData.stage === "done";
  if (!done) {
    resetLoginFlow();
    return;
  }
  const currentTab = document.querySelector('[data-user-tab="current"]');
  if (currentTab) currentTab.click();
}

async function switchUser() {
  const namespace = readSelectedNamespace();
  if (!namespace) return;
  if (!confirm(`切换到用户 ${namespace} 并重启 tdl？`)) return;
  try {
    const data = await api("/api/user/switch", {
      method: "POST",
      body: JSON.stringify({ namespace }),
    });
    setUserStatus(data.message || "正在切换用户。", "success");
  } catch (error) {
    setUserStatus(error.message, "error");
  }
}

async function deleteUser() {
  const namespace = readSelectedNamespace();
  if (!namespace) return;
  if (namespace === state.currentNamespace) {
    setUserStatusError("当前用户正在运行中，请先切换到其他用户后再删除。");
    return;
  }
  if (!confirm(`删除用户 ${namespace} 的登录数据？该用户将不再出现在切换列表中。`)) return;
  try {
    const data = await api("/api/user/delete", {
      method: "POST",
      body: JSON.stringify({ namespace }),
    });
    setUserStatus(data.message || "用户登录数据已删除。", "success");
    await loadUser();
  } catch (error) {
    setUserStatus(error.message, "error");
  }
}

function readSelectedNamespace() {
  const select = document.getElementById("switch-namespace");
  const namespace = (select && select.value ? select.value : "").trim();
  if (!namespace) {
    setUserStatusError("请选择一个已登录用户。");
    if (select) select.focus();
    return "";
  }
  if (!/^[A-Za-z]+$/.test(namespace)) {
    setUserStatusError("用户数据文件名无效，无法操作。");
    if (select) select.focus();
    return "";
  }
  return namespace;
}

function readNamespaceInput(id, showError) {
  const input = document.getElementById(id);
  const namespace = (input && input.value ? input.value : "").trim();
  if (!namespace) {
    showError("请先输入用户名。");
    if (input) input.focus();
    return "";
  }
  if (!/^[A-Za-z]+$/.test(namespace)) {
    showError("用户名只能使用英文字母。");
    if (input) input.focus();
    return "";
  }
  return namespace;
}

function setUserStatusError(message) {
  setUserStatus(message, "error");
}

function setUserStatus(message, kind = "") {
  const status = document.getElementById("user-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

function renderLoginError(message) {
  setLoginStatus(message, "error");
}

function setLoginStatus(message, kind = "") {
  const status = document.getElementById("login-status");
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

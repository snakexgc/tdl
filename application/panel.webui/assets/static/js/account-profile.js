import { api } from "./api.js";

let namespace,
  checkedAt = 0,
  generation = 0,
  pending;
const $ = (id) => document.getElementById(id);

export function renderAccountProfile(data) {
  if (!$("current-account")) return;
  const user = data.user || {};
  const name = data.valid
    ? user.name || (user.username ? `@${user.username}` : "Telegram 用户")
    : "未登录账号";
  $("account-name").textContent = name;
  $("account-detail").textContent = data.valid
    ? user.username
      ? `@${user.username}`
      : "当前 Telegram 账号"
    : "点击登录或检查会话";
  const words = name.trim().split(/\s+/);
  $("account-avatar").textContent = data.valid
    ? (words.length > 1
        ? words[0][0] + words.at(-1)[0]
        : Array.from(name).slice(0, 2).join("")
      ).toLocaleUpperCase()
    : "TG";
  $("current-account").setAttribute("aria-label", `${name}，查看账号管理`);
  $("current-account").title = `${name} · ${$("account-detail").textContent}`;
}

// A status heartbeat must not create a new Telegram session check each time.
export async function loadCurrentAccount(account) {
  if (namespace !== account) {
    namespace = account;
    checkedAt = 0;
    generation++;
  }
  if (pending || Date.now() - checkedAt < 60000) return;
  checkedAt = Date.now();
  const requested = namespace;
  const version = generation;
  pending = api("/api/user", { signal: AbortSignal.timeout(35000) });
  try {
    const data = await pending;
    if (requested === namespace && version === generation)
      renderAccountProfile(data);
  } catch {
    if (
      requested === namespace &&
      version === generation &&
      $("account-detail")
    ) {
      $("account-detail").textContent = "账号信息暂不可用";
    }
  } finally {
    pending = null;
  }
}

window.addEventListener("current-account-changed", (event) => {
  generation++;
  checkedAt = Date.now();
  renderAccountProfile(event.detail);
});

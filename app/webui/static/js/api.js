// Thin fetch wrapper. Redirects to the login page on 401 and normalizes errors.
export async function api(path, options = {}) {
  const response = await fetch(path, {
    credentials: "same-origin",
    headers: {
      "Content-Type": "application/json",
      ...(options.headers || {}),
    },
    ...options,
  });
  if (response.status === 401) {
    window.location.href = "/login";
    throw new Error("登录已过期，请重新登录。");
  }
  const text = await response.text();
  let data = {};
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = { error: text.trim() };
    }
  }
  if (!response.ok) {
    throw new Error(data.error || data.message || response.statusText);
  }
  return data;
}

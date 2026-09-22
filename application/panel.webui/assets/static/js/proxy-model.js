export const proxySchemes = ["socks5://", "socks5h://", "http://", "https://"];

// Keep the address as entered, including explicit default ports and IPv6 brackets.
export function parseProxy(value, fallback = proxySchemes[0]) {
  let address = String(value || "").trim();
  const scheme = proxySchemes.find((item) =>
    address.toLowerCase().startsWith(item),
  );
  if (!scheme && address.includes("://"))
    return { scheme: fallback, address, username: "", password: "" };
  if (scheme) address = address.slice(scheme.length).replace(/\/$/, "");
  let username = "",
    password = "";
  const at = address.lastIndexOf("@");
  if (at >= 0) {
    const credentials = address.slice(0, at);
    const colon = credentials.indexOf(":");
    username = decode(colon < 0 ? credentials : credentials.slice(0, colon));
    password = colon < 0 ? "" : decode(credentials.slice(colon + 1));
    address = address.slice(at + 1);
  }
  return { scheme: scheme || fallback, address, username, password };
}

function decode(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function serializeProxy({
  scheme,
  address,
  username = "",
  password = "",
}) {
  address = address.trim();
  if (!address && !username && !password) return "";
  const credentials =
    username || password
      ? `${encodeURIComponent(username)}:${encodeURIComponent(password)}@`
      : "";
  return `${scheme}${credentials}${address}`;
}

export function proxyAddressError({ address, username, password }) {
  if (!address) return username || password ? "请填写代理地址和端口。" : "";
  const match = address.match(/^(\[[^\]]+\]|[^:[\]\s/@?#\\]+):(\d+)$/);
  if (match && Number(match[2]) >= 1 && Number(match[2]) <= 65535) {
    try {
      if (new URL(`http://${address}`).hostname) return "";
    } catch {
      /* The message below also covers malformed IPv6 addresses. */
    }
  }
  return "请填写 IP 或域名加端口，例如 127.0.0.1:1080；IPv6 使用 [::1]:1080。";
}

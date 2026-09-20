import { element } from "./ui.js";
import {
  proxySchemes,
  parseProxy,
  serializeProxy,
  proxyAddressError,
} from "./proxy-model.js";

export function createProxyControl(value, { id, helpID, onChange }) {
  const root = element("div", null, "proxy-editor");
  const control = element("div", null, "proxy-control");
  const scheme = element("select");
  scheme.dataset.proxyScheme = "";
  scheme.setAttribute("aria-label", "代理协议");
  for (const name of proxySchemes) {
    const option = element("option", name);
    option.value = name;
    scheme.append(option);
  }
  const address = element("input");
  address.id = id;
  address.type = "text";
  address.dataset.proxyAddress = "";
  address.placeholder = "127.0.0.1:1080";
  address.autocomplete = "off";
  address.spellcheck = false;
  address.setAttribute("aria-describedby", helpID);
  control.append(scheme, address);

  const auth = element("details", null, "proxy-auth");
  auth.append(element("summary", "代理认证（可选）"));
  const fields = element("div", null, "proxy-auth-fields");
  function credential(name, title, type) {
    const label = element("label", title);
    const input = element("input");
    input.id = `${id}-${name}`;
    input.type = type;
    input.autocomplete = type === "password" ? "new-password" : "off";
    input.spellcheck = false;
    label.append(input);
    fields.append(label);
    return input;
  }
  const username = credential("username", "代理用户名", "text");
  const password = credential("password", "代理密码", "password");
  auth.append(fields);
  root.append(control, auth);

  function fill(proxy) {
    scheme.value = proxy.scheme;
    address.value = proxy.address;
    username.value = proxy.username;
    password.value = proxy.password;
    if (proxy.username || proxy.password) auth.open = true;
  }
  function current() {
    return {
      scheme: scheme.value,
      address: address.value.trim(),
      username: username.value,
      password: password.value,
    };
  }
  function changed() {
    // Pasted URLs become separate controls without exposing credentials in the address.
    if (address.value.includes("://") || address.value.includes("@"))
      fill(parseProxy(address.value, scheme.value));
    address.setCustomValidity(proxyAddressError(current()));
    onChange(serializeProxy(current()));
  }
  fill(parseProxy(value));
  address.setCustomValidity(proxyAddressError(current()));
  root.addEventListener("input", changed);
  root.addEventListener("change", changed);
  return root;
}

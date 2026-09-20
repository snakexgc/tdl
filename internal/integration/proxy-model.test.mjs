import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import vm from "node:vm";

const module = new vm.SourceTextModule(
  await readFile(
    new URL(
      "../../application/panel.webui/assets/static/js/proxy-model.js",
      import.meta.url,
    ),
    "utf8",
  ),
  { context: vm.createContext({ URL }) },
);
await module.link(() => {
  throw new Error("Proxy model must be self-contained");
});
await module.evaluate();
const { proxySchemes, parseProxy, serializeProxy, proxyAddressError } =
  module.namespace;

test("proxy controls preserve protocols, explicit default ports and IPv6 when saving", () => {
  for (const scheme of proxySchemes) {
    for (const address of ["127.0.0.1:1080", "proxy.example:80", "[::1]:443"]) {
      const value = scheme + address;
      const parsed = parseProxy(value);
      assert.equal(parsed.address, address);
      assert.equal(proxyAddressError(parsed), "");
      assert.equal(serializeProxy(parsed), value);
    }
  }
  assert.equal(parseProxy("HTTPS://proxy.example:443/").scheme, "https://");
  assert.equal(
    serializeProxy(parseProxy(" 127.0.0.1:1080 ", "http://")),
    "http://127.0.0.1:1080",
  );
});

test("pasted proxy credentials are separated and encoded without being lost from the draft", () => {
  const draft = {
    scheme: "socks5h://",
    address: "[::1]:1080",
    username: "a@b: 用户",
    password: "p@ss:/?#% word",
  };
  const value = serializeProxy(draft);
  const parsed = parseProxy(value);
  for (const key of Object.keys(draft)) assert.equal(parsed[key], draft[key]);
  assert.equal(parsed.address.includes("@"), false);
  assert.equal(serializeProxy(parsed), value);
  assert.doesNotThrow(() => parseProxy("socks5://user:%broken@localhost:1080"));
  assert.notEqual(
    proxyAddressError(parseProxy("ftp://user:pass@host:1080")),
    "",
  );
});

test("blank proxy preserves the saved secret, incomplete or malformed addresses cannot be submitted", () => {
  const blank = parseProxy("");
  assert.equal(serializeProxy(blank), "");
  assert.equal(proxyAddressError(blank), "");
  for (const address of [
    "localhost",
    "localhost:0",
    "localhost:65536",
    "localhost:abc",
    "::1:1080",
    "[invalid]:1080",
    "host:1080/path",
    "host:1080?query",
    "ftp://host:1080",
  ]) {
    assert.notEqual(proxyAddressError({ ...blank, address }), "", address);
  }
  const incomplete = { ...blank, username: "user", password: "secret" };
  const restored = parseProxy(serializeProxy(incomplete));
  assert.equal(restored.password, "secret");
  assert.notEqual(proxyAddressError(restored), "");
});

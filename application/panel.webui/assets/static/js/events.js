// One connection per panel. HTTP remains the command/snapshot API; polling is
// only a bounded fallback when a proxy does not support WebSocket upgrades.
const listeners = new Map();
const endpoints = { status: "/api/status", dashboard: "/api/dashboard", downloads: "/api/internal-downloads", forwards: "/api/forwards" };
endpoints["download-tasks-local"] = "/api/download-tasks?executor=local";
endpoints["download-tasks-aria2"] = "/api/download-tasks?executor=aria2";
let socket, reconnect, fallback, stopped = true, attempts = 0;
const pending = new Set();

function indicator(value) {
  const element = document.getElementById("heartbeat-indicator");
  if (element) {
    element.dataset.state = value;
    element.title = { online: "已连接，实时接收更新", offline: "连接已断开，正在自动重连", checking: "正在建立连接", fallback: "已连接，定时获取更新" }[value];
  }
  const label = document.getElementById("heartbeat-label");
  if (label) label.textContent = value === "online" || value === "fallback" ? "已连接" : "断开连接";
}

function publish(topic, packet) {
  for (const callback of listeners.get(topic) || []) {
    try { callback(packet.data, packet.error); } catch (error) { console.error(error); }
  }
}

function subscribe() {
  if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ topics: [...listeners.keys()] }));
}

export function observe(topic, callback) {
  if (!endpoints[topic]) throw new Error(`Unknown observation: ${topic}`);
  if (!listeners.has(topic)) listeners.set(topic, new Set());
  listeners.get(topic).add(callback);
  subscribe();
  return () => {
    listeners.get(topic)?.delete(callback);
    if (!listeners.get(topic)?.size) listeners.delete(topic);
    subscribe();
  };
}

async function pollFallback() {
  if (stopped || socket?.readyState === WebSocket.OPEN || document.hidden) return;
  for (const topic of listeners.keys()) {
    if (pending.has(topic)) continue;
    pending.add(topic);
    fetch(endpoints[topic], { credentials: "same-origin", cache: "no-store", signal: AbortSignal.timeout(4000) })
      .then(async response => {
        if (response.status === 401) { stopRealtime(); window.location.href = "/login"; return; }
        if (!response.ok) throw new Error(response.statusText);
        const data = await response.json();
        if (stopped || socket?.readyState === WebSocket.OPEN) return;
        publish(topic, { data });
        if (socket?.readyState !== WebSocket.OPEN) indicator("fallback");
      }).catch(error => { if (!stopped && socket?.readyState !== WebSocket.OPEN) { publish(topic, { error: error.message }); indicator("offline"); } }).finally(() => pending.delete(topic));
  }
}

function connect() {
  if (stopped) return;
  const url = new URL("/api/events", window.location.href);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  const connection = new WebSocket(url);
  socket = connection;
  connection.onopen = () => {
    if (socket !== connection || stopped) { connection.close(); return; }
    attempts = 0;
    indicator("online");
    subscribe();
  };
  connection.onmessage = event => {
    if (stopped || socket !== connection) return;
    try { const packet = JSON.parse(event.data); publish(packet.topic, packet); } catch (error) { console.error(error); }
  };
  connection.onclose = event => {
    if (socket !== connection || stopped) return;
    if (event.code === 1008) { stopRealtime(); window.location.href = "/login"; return; }
    indicator("offline");
    pollFallback();
    reconnect = setTimeout(connect, Math.min(30000, 1000 * 2 ** attempts++) + Math.random() * 500);
  };
  connection.onerror = () => connection.close();
}

export function startRealtime() {
  if (!stopped) return;
  stopped = false;
  indicator("checking");
  connect();
  fallback = setInterval(pollFallback, 5000);
}

export function stopRealtime() {
  stopped = true;
  clearTimeout(reconnect);
  clearInterval(fallback);
  socket?.close();
  socket = null;
}

window.addEventListener("pagehide", stopRealtime);
window.addEventListener("pageshow", event => { if (event.persisted) startRealtime(); });

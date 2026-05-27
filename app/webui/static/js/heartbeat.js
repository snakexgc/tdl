// Sidebar heartbeat indicator: polls /api/heartbeat and reflects online/offline.
import { state } from "./state.js";

const heartbeatIntervalMS = 1000;
const heartbeatOfflineMS = 3000;
const heartbeatRequestTimeoutMS = 1500;

export function startHeartbeat() {
  state.heartbeatLastSeen = Date.now();
  setHeartbeatState("checking");
  runHeartbeat();
  state.heartbeatTimer = window.setInterval(runHeartbeat, heartbeatIntervalMS);
}

export function stopHeartbeat() {
  if (!state.heartbeatTimer) return;
  window.clearInterval(state.heartbeatTimer);
  state.heartbeatTimer = null;
}

async function runHeartbeat() {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), heartbeatRequestTimeoutMS);
  try {
    const response = await fetch("/api/heartbeat", {
      credentials: "same-origin",
      signal: controller.signal,
      cache: "no-store",
    });
    if (response.status === 401) {
      window.location.href = "/login";
      return;
    }
    if (!response.ok) {
      throw new Error(response.statusText);
    }
    state.heartbeatLastSeen = Date.now();
    setHeartbeatState("online");
  } catch {
    const elapsed = state.heartbeatLastSeen ? Date.now() - state.heartbeatLastSeen : heartbeatOfflineMS;
    if (elapsed >= heartbeatOfflineMS) {
      setHeartbeatState("offline");
    }
  } finally {
    window.clearTimeout(timeout);
  }
}

function setHeartbeatState(next) {
  if (state.heartbeatState === next) return;
  state.heartbeatState = next;
  const indicator = document.getElementById("heartbeat-indicator");
  const label = document.getElementById("heartbeat-label");
  if (!indicator || !label) return;
  indicator.dataset.state = next;
  if (next === "online") {
    label.textContent = "TDL 在线";
  } else if (next === "offline") {
    label.textContent = "TDL 离线";
  } else {
    label.textContent = "TDL 连接中";
  }
}

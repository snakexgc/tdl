// Shared, dependency-free helpers: formatting, escaping and small DOM utilities.
export const collator = new Intl.Collator("zh-Hans-CN", {
  numeric: true,
  sensitivity: "base",
});

export function getPath(obj, path) {
  return path.split(".").reduce((current, part) => current && current[part], obj);
}

export function splitList(value) {
  return String(value || "").split(/[\s,]+/).map((item) => item.trim()).filter(Boolean);
}

export function formatTime(value) {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  return date.toLocaleString();
}

export function setText(id, value) {
  const target = document.getElementById(id);
  if (!target) return;
  target.textContent = value;
}

export function formatPercent(value) {
  const percent = Number(value || 0);
  if (!Number.isFinite(percent)) return "-";
  return `${percent.toFixed(1)}%`;
}

export function formatCount(value) {
  const count = Number(value || 0);
  if (!Number.isFinite(count)) return "-";
  return Math.max(0, Math.trunc(count)).toLocaleString();
}

export function formatBytes(value) {
  let size = Number(value || 0);
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let index = 0;
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024;
    index += 1;
  }
  return `${size.toFixed(index === 0 ? 0 : 2)} ${units[index]}`;
}

export function clampNumber(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

export function infoItem(label, value) {
  return `
    <div class="info-item">
      <div class="info-label">${escapeHTML(label)}</div>
      <div class="info-value">${escapeHTML(String(value))}</div>
    </div>
  `;
}

export function escapeHTML(value) {
  return String(value ?? "").replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

export function escapeAttr(value) {
  return escapeHTML(value).replace(/`/g, "&#96;");
}

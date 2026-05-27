// Dashboard view: live CPU/memory/speed charts and download stat bars.
import { state } from "./state.js";
import { api } from "./api.js";
import {
  setText,
  formatBytes,
  formatPercent,
  formatCount,
  formatTime,
  escapeHTML,
  clampNumber,
} from "./utils.js";

const dashboardRefreshMS = 1000;

export function initDashboard() {
  document.getElementById("refresh-dashboard").addEventListener("click", () => loadDashboard({ force: true }));
}

export async function loadDashboard(options = {}) {
  if (state.dashboardLoading) return;
  state.dashboardLoading = true;
  const silent = Boolean(options.silent);
  if (!silent) setDashboardStatus("正在刷新...");
  try {
    const data = await api("/api/dashboard");
    pushDashboardSample(data);
    renderDashboard();
    const sampledAt = data.sampled_at ? formatTime(data.sampled_at) : formatTime(new Date().toISOString());
    const errors = data.errors ? Object.values(data.errors).filter(Boolean) : [];
    setDashboardStatus(errors.length ? `部分指标读取失败：${errors.join("；")}` : `更新于 ${sampledAt}`, errors.length ? "warn" : "");
    startDashboardPolling();
  } catch (error) {
    setDashboardStatus(error.message, "error");
  } finally {
    state.dashboardLoading = false;
  }
}

function startDashboardPolling() {
  if (state.dashboardPoll) return;
  state.dashboardPoll = window.setInterval(() => {
    const view = document.getElementById("view-dashboard");
    if (!view || !view.classList.contains("active")) {
      stopDashboardPolling();
      return;
    }
    loadDashboard({ silent: true });
  }, dashboardRefreshMS);
}

export function stopDashboardPolling() {
  if (!state.dashboardPoll) return;
  window.clearInterval(state.dashboardPoll);
  state.dashboardPoll = null;
}

function pushDashboardSample(data) {
  const process = data.process || {};
  const memory = data.memory || {};
  const download = data.download || {};
  const httpMetrics = data.http || {};
  const aria2 = data.aria2 || {};
  const totalMemory = Number(memory.total_bytes ?? process.memory_rss ?? 0);
  const bufferMemory = Number(memory.buffer_bytes ?? 0);
  const retainedMemory = Number(memory.heap_retained_idle_bytes ?? 0);
  const softwareMemory = Number(memory.software_bytes ?? Math.max(0, totalMemory - bufferMemory));
  const gotdSpeed = Number(download.gotd_speed_bps ?? download.speed_bps ?? 0);

  state.dashboardSamples.push({
    at: data.sampled_at ? new Date(data.sampled_at).getTime() : Date.now(),
    cpu: Number(process.cpu_percent || 0),
    goroutines: Number(process.goroutines || 0),
    memoryTotal: totalMemory,
    memorySoftware: softwareMemory,
    memoryBuffer: bufferMemory,
    memoryRetained: retainedMemory,
    memoryPercent: Number(memory.total_percent || process.memory_percent || 0),
    gotdSpeed,
    gotdTotal: Number(download.gotd_bytes_total ?? download.bytes_total ?? 0),
    aria2Speed: Number(aria2.speed_bps ?? download.aria2_speed_bps ?? 0),
    aria2Available: Boolean(aria2.available ?? download.aria2_available),
    activeChunks: Number(httpMetrics.active_chunk_requests ?? download.active_chunk_requests ?? 0),
    fileErrors: Number(httpMetrics.telegram_file_errors ?? download.telegram_file_errors ?? 0),
    fileErrors10s: Number(httpMetrics.telegram_file_errors_10s ?? download.telegram_file_errors_10s ?? 0),
    aria2Tasks: Number(aria2.task_count ?? download.aria2_task_count ?? 0),
    aria2ActiveTasks: Number(aria2.active_tasks ?? download.aria2_active_tasks ?? 0),
    aria2WaitingTasks: Number(aria2.waiting_tasks ?? download.aria2_waiting_tasks ?? 0),
    aria2StoppedTasks: Number(aria2.stopped_tasks ?? download.aria2_stopped_tasks ?? 0),
  });
  if (state.dashboardSamples.length > 30) {
    state.dashboardSamples.splice(0, state.dashboardSamples.length - 30);
  }
}

function renderDashboard() {
  const latest = state.dashboardSamples[state.dashboardSamples.length - 1];
  if (!latest) return;

  setText("dashboard-cpu-value", formatPercent(latest.cpu));
  setText("dashboard-cpu-meta", `${latest.goroutines} goroutines`);
  setText("dashboard-memory-value", formatBytes(latest.memoryTotal));
  setText("dashboard-memory-meta", `软件 ${formatBytes(latest.memorySoftware)} · 缓冲 ${formatBytes(latest.memoryBuffer)} · 保留堆 ${formatBytes(latest.memoryRetained)}`);
  setText("dashboard-speed-value", `${formatBytes(latest.gotdSpeed)}/s`);
  setText("dashboard-speed-meta", `aria2 ${formatBytes(latest.aria2Speed)}/s · gotd累计 ${formatBytes(latest.gotdTotal)}`);
  setText("dashboard-active-chunks-value", formatCount(latest.activeChunks));
  setText("dashboard-file-errors-value", formatCount(latest.fileErrors));
  setText("dashboard-file-errors-10s-value", formatCount(latest.fileErrors10s));
  setText("dashboard-aria2-active-value", formatCount(latest.aria2ActiveTasks));
  setText("dashboard-aria2-waiting-value", formatCount(latest.aria2WaitingTasks));
  setText("dashboard-aria2-stopped-value", formatCount(latest.aria2StoppedTasks));
  setText(
    "dashboard-aria2-tasks-meta",
    `aria2：活动 ${formatCount(latest.aria2ActiveTasks)} · 等待 ${formatCount(latest.aria2WaitingTasks)} · 停止 ${formatCount(latest.aria2StoppedTasks)}`
  );
  renderDashboardStatBars(latest);

  renderSmoothChart("dashboard-cpu-chart", "dashboard-cpu-axis", [
    { key: "cpu", label: "CPU", color: "#0b7f72" },
  ], { min: 0, formatter: formatPercent });

  renderSmoothChart("dashboard-memory-chart", "dashboard-memory-axis", [
    { key: "memoryTotal", label: "总用量", color: "#0b7f72" },
    { key: "memorySoftware", label: "软件用量", color: "#6f5cc2" },
    { key: "memoryBuffer", label: "HTTP buffer", color: "#c47a16" },
    { key: "memoryRetained", label: "保留堆", color: "#6a7380" },
  ], { min: 0, formatter: formatBytes });

  renderSmoothChart("dashboard-speed-chart", "dashboard-speed-axis", [
    { key: "gotdSpeed", label: "gotd", color: "#0b7f72" },
    { key: "aria2Speed", label: "aria2", color: "#c47a16" },
  ], { min: 0, formatter: (value) => `${formatBytes(value)}/s` });
}

function renderDashboardStatBars(latest) {
  const max = Math.max(
    1,
    Number(latest.activeChunks || 0),
    Number(latest.fileErrors10s || 0),
    Number(latest.fileErrors || 0),
    Number(latest.aria2ActiveTasks || 0),
    Number(latest.aria2WaitingTasks || 0),
    Number(latest.aria2StoppedTasks || 0)
  );
  setDashboardStatBar("dashboard-active-chunks-bar", latest.activeChunks, max);
  setDashboardStatBar("dashboard-file-errors-10s-bar", latest.fileErrors10s, max);
  setDashboardStatBar("dashboard-file-errors-bar", latest.fileErrors, max);
  setDashboardStatBar("dashboard-aria2-active-bar", latest.aria2ActiveTasks, max);
  setDashboardStatBar("dashboard-aria2-waiting-bar", latest.aria2WaitingTasks, max);
  setDashboardStatBar("dashboard-aria2-stopped-bar", latest.aria2StoppedTasks, max);
}

function setDashboardStatBar(id, value, max) {
  const target = document.getElementById(id);
  if (!target) return;
  const numeric = Math.max(0, Number(value || 0));
  const percent = max > 0 ? Math.min(100, Math.max(2, (numeric / max) * 100)) : 0;
  target.style.width = numeric > 0 ? `${percent}%` : "0";
}

function renderSmoothChart(svgID, axisID, series, options = {}) {
  const svg = document.getElementById(svgID);
  const axis = document.getElementById(axisID);
  if (!svg) return;
  if (axis) axis.textContent = "";

  const bounds = svg.getBoundingClientRect();
  const width = Math.max(320, Math.round(bounds.width || 640));
  const height = Math.max(140, Math.round(bounds.height || 160));
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.removeAttribute("preserveAspectRatio");

  const padding = { top: 10, right: 12, bottom: 24, left: 74 };
  const plotWidth = width - padding.left - padding.right;
  const plotHeight = height - padding.top - padding.bottom;
  const range = dashboardChartRange(series, options);
  const slotCount = 30;
  const slotOffset = Math.max(0, slotCount - state.dashboardSamples.length);
  const formatter = options.formatter || ((value) => String(value));

  const yFor = (value) => {
    const ratio = (Number(value || 0) - range.min) / (range.max - range.min);
    return padding.top + (1 - Math.min(1, Math.max(0, ratio))) * plotHeight;
  };
  const xFor = (index) => padding.left + ((slotOffset + index) / (slotCount - 1)) * plotWidth;

  const grid = [0, 0.25, 0.5, 0.75, 1].map((ratio) => {
    const y = padding.top + ratio * plotHeight;
    return `<line class="grid-line" x1="${padding.left}" y1="${y.toFixed(2)}" x2="${(width - padding.right).toFixed(2)}" y2="${y.toFixed(2)}"></line>`;
  }).join("");

  const axisValues = [range.max, range.min + (range.max - range.min) / 2, range.min];
  const yAxisLabels = axisValues.map((value) => {
    const y = yFor(value);
    return [
      `<line class="axis-tick" x1="${(padding.left - 4).toFixed(2)}" y1="${y.toFixed(2)}" x2="${padding.left.toFixed(2)}" y2="${y.toFixed(2)}"></line>`,
      `<text class="axis-label" x="${(padding.left - 8).toFixed(2)}" y="${(y + 4).toFixed(2)}" text-anchor="end">${escapeHTML(formatter(value))}</text>`,
    ].join("");
  }).join("");
  const xAxisY = padding.top + plotHeight;
  const axisLines = [
    `<line class="axis-line" x1="${padding.left}" y1="${padding.top}" x2="${padding.left}" y2="${xAxisY.toFixed(2)}"></line>`,
    `<line class="axis-line" x1="${padding.left}" y1="${xAxisY.toFixed(2)}" x2="${(width - padding.right).toFixed(2)}" y2="${xAxisY.toFixed(2)}"></line>`,
    `<line class="axis-tick" x1="${padding.left}" y1="${xAxisY.toFixed(2)}" x2="${padding.left}" y2="${(xAxisY + 5).toFixed(2)}"></line>`,
    `<line class="axis-tick" x1="${(width - padding.right).toFixed(2)}" y1="${xAxisY.toFixed(2)}" x2="${(width - padding.right).toFixed(2)}" y2="${(xAxisY + 5).toFixed(2)}"></line>`,
    `<text class="axis-label" x="${padding.left}" y="${(height - 5).toFixed(2)}" text-anchor="start">30s</text>`,
    `<text class="axis-label" x="${(width - padding.right).toFixed(2)}" y="${(height - 5).toFixed(2)}" text-anchor="end">现在</text>`,
  ].join("");

  const paths = series.map((item) => {
    const points = state.dashboardSamples.map((sample, index) => ({
      x: xFor(index),
      y: yFor(sample[item.key]),
    }));
    if (!points.length) return "";
    const d = smoothDashboardPath(points);
    const point = points.length === 1 ? `<circle cx="${points[0].x.toFixed(2)}" cy="${points[0].y.toFixed(2)}" r="3.5" fill="${item.color}"></circle>` : "";
    return `<path class="series-line" d="${d}" stroke="${item.color}"></path>${point}`;
  }).join("");
  const hoverLayer = `<g class="chart-hover-layer"></g><rect class="chart-hit-area" x="${padding.left}" y="${padding.top}" width="${plotWidth}" height="${plotHeight}"></rect>`;

  svg.innerHTML = grid + axisLines + yAxisLabels + paths + hoverLayer;
  bindDashboardChartHover(svg, {
    formatter,
    height,
    padding,
    plotHeight,
    plotWidth,
    series,
    slotCount,
    slotOffset,
    width,
    xAxisY,
    xFor,
    yFor,
  });
}

function bindDashboardChartHover(svg, chart) {
  const hitArea = svg.querySelector(".chart-hit-area");
  const hoverLayer = svg.querySelector(".chart-hover-layer");
  if (!hitArea || !hoverLayer || !state.dashboardSamples.length) return;

  hitArea.addEventListener("mousemove", (event) => {
    const rect = svg.getBoundingClientRect();
    const rawX = event.clientX - rect.left;
    const x = Math.min(chart.width - chart.padding.right, Math.max(chart.padding.left, rawX));
    const virtualIndex = ((x - chart.padding.left) / chart.plotWidth) * (chart.slotCount - 1) - chart.slotOffset;
    const index = Math.min(state.dashboardSamples.length - 1, Math.max(0, Math.round(virtualIndex)));
    renderDashboardChartHover(hoverLayer, chart, index);
  });
  hitArea.addEventListener("mouseleave", () => {
    hoverLayer.innerHTML = "";
  });
}

function renderDashboardChartHover(layer, chart, index) {
  const sample = state.dashboardSamples[index];
  if (!sample) return;

  const pointX = chart.xFor(index);
  const rows = chart.series.map((item) => ({
    color: item.color,
    label: item.label || item.key,
    value: chart.formatter(sample[item.key] || 0),
    y: chart.yFor(sample[item.key]),
  }));
  const title = dashboardSampleLabel(sample);
  const labels = [title, ...rows.map((row) => `${row.label}: ${row.value}`)];
  const tooltipWidth = Math.max(126, Math.min(260, Math.max(...labels.map(estimatedTextWidth)) + 30));
  const tooltipHeight = 28 + rows.length * 18;
  let tooltipX = pointX + 12;
  if (tooltipX + tooltipWidth > chart.width - 6) {
    tooltipX = pointX - tooltipWidth - 12;
  }
  tooltipX = Math.max(6, tooltipX);
  const tooltipY = chart.padding.top + 6;

  const points = rows.map((row) => (
    `<circle class="hover-point" cx="${pointX.toFixed(2)}" cy="${row.y.toFixed(2)}" r="4.2" fill="${row.color}"></circle>`
  )).join("");
  const tooltipRows = rows.map((row, rowIndex) => {
    const y = tooltipY + 40 + rowIndex * 18;
    return [
      `<circle cx="${(tooltipX + 12).toFixed(2)}" cy="${(y - 4).toFixed(2)}" r="3.4" fill="${row.color}"></circle>`,
      `<text class="hover-tooltip-text" x="${(tooltipX + 22).toFixed(2)}" y="${y.toFixed(2)}">${escapeHTML(row.label)}: ${escapeHTML(row.value)}</text>`,
    ].join("");
  }).join("");

  layer.innerHTML = [
    `<line class="hover-line" x1="${pointX.toFixed(2)}" y1="${chart.padding.top}" x2="${pointX.toFixed(2)}" y2="${chart.xAxisY.toFixed(2)}"></line>`,
    points,
    `<g class="hover-tooltip">`,
    `<rect class="hover-tooltip-bg" x="${tooltipX.toFixed(2)}" y="${tooltipY.toFixed(2)}" width="${tooltipWidth.toFixed(2)}" height="${tooltipHeight.toFixed(2)}" rx="7" ry="7"></rect>`,
    `<text class="hover-tooltip-title" x="${(tooltipX + 12).toFixed(2)}" y="${(tooltipY + 20).toFixed(2)}">${escapeHTML(title)}</text>`,
    tooltipRows,
    `</g>`,
  ].join("");
}

function dashboardSampleLabel(sample) {
  const latest = state.dashboardSamples[state.dashboardSamples.length - 1];
  if (!latest || !sample.at || !latest.at) return "采样点";
  const seconds = Math.max(0, Math.round((latest.at - sample.at) / 1000));
  return seconds === 0 ? "现在" : `${seconds}s 前`;
}

function estimatedTextWidth(text) {
  return Array.from(String(text || "")).reduce((width, char) => width + (char.charCodeAt(0) > 255 ? 12 : 6.5), 0);
}

function dashboardChartRange(series, options = {}) {
  const values = [];
  state.dashboardSamples.forEach((sample) => {
    series.forEach((item) => {
      const value = Number(sample[item.key] || 0);
      if (Number.isFinite(value)) values.push(value);
    });
  });
  const configuredMin = Number(options.min ?? 0);
  const min = Number.isFinite(configuredMin) ? configuredMin : 0;
  const maxValue = Math.max(...values, min);
  const max = niceCeil(Math.max(maxValue * 1.15, min + 1));
  return { min, max: max <= min ? min + 1 : max };
}

function niceCeil(value) {
  if (!Number.isFinite(value) || value <= 0) return 1;
  const power = 10 ** Math.floor(Math.log10(value));
  const scaled = value / power;
  let nice = 10;
  if (scaled <= 1) nice = 1;
  else if (scaled <= 2) nice = 2;
  else if (scaled <= 5) nice = 5;
  return nice * power;
}

function smoothDashboardPath(points) {
  const formatPoint = (point) => `${point.x.toFixed(2)} ${point.y.toFixed(2)}`;
  if (points.length === 1) return `M ${formatPoint(points[0])}`;
  if (points.length === 2) {
    const [a, b] = points;
    const midX = (a.x + b.x) / 2;
    return `M ${formatPoint(a)} C ${midX.toFixed(2)} ${a.y.toFixed(2)}, ${midX.toFixed(2)} ${b.y.toFixed(2)}, ${formatPoint(b)}`;
  }

  let d = `M ${formatPoint(points[0])}`;
  for (let index = 0; index < points.length - 1; index += 1) {
    const p0 = points[index - 1] || points[index];
    const p1 = points[index];
    const p2 = points[index + 1];
    const p3 = points[index + 2] || p2;
    const cp1 = {
      x: p1.x + (p2.x - p0.x) / 6,
      y: p1.y + (p2.y - p0.y) / 6,
    };
    const cp2 = {
      x: p2.x - (p3.x - p1.x) / 6,
      y: p2.y - (p3.y - p1.y) / 6,
    };
    const minY = Math.min(p1.y, p2.y);
    const maxY = Math.max(p1.y, p2.y);
    cp1.y = clampNumber(cp1.y, minY, maxY);
    cp2.y = clampNumber(cp2.y, minY, maxY);
    d += ` C ${formatPoint(cp1)}, ${formatPoint(cp2)}, ${formatPoint(p2)}`;
  }
  return d;
}

function setDashboardStatus(message, kind = "") {
  const status = document.getElementById("dashboard-status");
  if (!status) return;
  status.className = `notice ${kind}`.trim();
  status.textContent = message || "";
}

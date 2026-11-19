// Dashboard view: live process metrics and task counts.
import { state, trackedExecutor } from "./state.js";
import { api } from "./api.js";
import { currentDownloader } from "./status.js";
import {
  setText,
  formatBytes,
  formatPercent,
  formatCount,
  formatTime,
  escapeHTML,
} from "./utils.js";

import { observe } from "./events.js";
const historyWindow = 30_000;
const sampleClock = new Intl.DateTimeFormat("zh-CN", { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
const speedText = value => `${formatBytes(value)}/s`;
const reading = (value, formatter) => value == null ? "—" : formatter(value);
const chartPointers = new WeakMap();
let unobserve;
let dashboardGeneration = 0;
let overviewReleases = [];
let overviewGeneration = 0;

export function initDashboard() {
  document.getElementById("refresh-dashboard").addEventListener("click", () => loadDashboard({ force: true }));
  document.getElementById("dashboard-history").addEventListener("toggle", renderDashboardHistory);
}

export async function loadDashboard(options = {}) {
  startDashboardPolling();
  if (state.dashboardLoading) return;
  void loadOverview();
  const generation = ++dashboardGeneration;
  const previousSample = state.dashboardSamples.at(-1);
  state.dashboardLoading = true;
  const silent = Boolean(options.silent);
  const button = document.getElementById("refresh-dashboard");
  if (button) button.disabled = true;
  if (!silent) setDashboardStatus("正在刷新…");
  try {
    const data = await api("/api/dashboard");
    if (generation === dashboardGeneration) receiveDashboard(data);
  } catch (error) {
    if (generation === dashboardGeneration && previousSample === state.dashboardSamples.at(-1)) receiveDashboard(null, error.message);
  } finally {
    if (generation === dashboardGeneration) {
      state.dashboardLoading = false;
      if (button) button.disabled = false;
    }
  }
}

function receiveDashboard(data, error) {
  if (error) {
    const hasHistory = state.dashboardSamples.length > 0;
    if (!hasHistory) {
      setText("dashboard-updated", "尚未收到采样");
      setText("dashboard-cpu-meta", "协程数未获取");
      setText("dashboard-speed-meta", "累计下载量未获取");
      setText("dashboard-dc-schedulers-meta", "DC 调度器状态未获取");
    }
    const message = hasHistory ? "采样更新中断，显示上次结果" : "运行指标读取失败";
    setDashboardStatus(`${message}：${error}`, "error");
    return;
  }
  if (!pushDashboardSample(data)) return;
  renderDashboard();
  const latest = state.dashboardSamples.at(-1);
  setText("dashboard-updated", `最近采样 ${formatTime(latest.at)}`);
  const errors = Object.values(data.errors || {}).filter(Boolean);
  setDashboardStatus(errors.length ? `部分指标读取失败：${errors.join("；")}` : "", errors.length ? "warn" : "");
}
function startDashboardPolling() {
  unobserve ||= observe("dashboard", receiveDashboard);
  window.addEventListener("downloader-changed", loadOverview);
}
export function stopDashboardPolling() {
  window.removeEventListener("downloader-changed", loadOverview);
  unobserve?.();
  unobserve = null;
  ++dashboardGeneration;
  state.dashboardLoading = false;
  stopOverview();
}

function pushDashboardSample(data) {
  const at = Date.parse(data.sampled_at) || Date.now();
  const samples = state.dashboardSamples;
  // A manual snapshot can arrive after the next realtime sample.
  if (samples.length && samples.at(-1).at > at) return false;
  if (samples.at(-1)?.at === at) samples.pop();
  const process = data.process || {};
  const memory = data.memory || {};
  const download = data.download || {};
  const httpMetrics = data.http || {};
  const dcSchedulers = Array.isArray(httpMetrics.dc_schedulers) ? httpMetrics.dc_schedulers : [];
  const totalMemory = Number(memory.total_bytes ?? process.memory_rss ?? 0);
  const retainedMemory = Number(memory.heap_retained_idle_bytes ?? 0);
  const softwareMemory = Number(memory.software_bytes ?? totalMemory);
  const gotdSpeed = Number(download.gotd_speed_bps ?? download.speed_bps ?? 0);

  samples.push({
    at,
    cpu: data.errors?.cpu ? null : Number(process.cpu_percent || 0),
    goroutines: Number(process.goroutines || 0),
    memoryTotal: data.errors?.memory ? null : totalMemory,
    memorySoftware: data.errors?.memory ? null : softwareMemory,
    memoryRetained: retainedMemory,
    memoryPercent: Number(memory.total_percent || process.memory_percent || 0),
    gotdSpeed,
    gotdTotal: Number(download.gotd_bytes_total ?? download.bytes_total ?? 0),
    activeChunks: Number(httpMetrics.active_chunk_requests ?? download.active_chunk_requests ?? 0),
    dcSchedulers,
    fileErrors: Number(httpMetrics.telegram_file_errors ?? download.telegram_file_errors ?? 0),
    fileErrors10s: Number(httpMetrics.telegram_file_errors_10s ?? download.telegram_file_errors_10s ?? 0),
  });
  while (samples[0].at < at - historyWindow) samples.shift();
  return true;
}

function renderDashboard() {
  const latest = state.dashboardSamples[state.dashboardSamples.length - 1];
  if (!latest) return;

  setText("dashboard-cpu-value", reading(latest.cpu, formatPercent));
  setText("dashboard-cpu-meta", `Go 协程 ${formatCount(latest.goroutines)}`);
  setText("dashboard-memory-value", reading(latest.memoryTotal, formatBytes));
  setText("dashboard-speed-value", speedText(latest.gotdSpeed));
  setText("dashboard-speed-meta", `gotd 累计 ${formatBytes(latest.gotdTotal)}`);
  setText("dashboard-active-chunks-value", formatCount(latest.activeChunks));
  setText("dashboard-dc-schedulers-meta", formatDCSchedulers(latest.dcSchedulers));
  setText("dashboard-file-errors-value", formatCount(latest.fileErrors));
  setErrorCount("dashboard-file-errors-10s-value", latest.fileErrors10s);

  renderChart("dashboard-cpu-chart", [
    { key: "cpu", label: "CPU", color: "var(--chart-1)" },
  ], { min: 0, formatter: formatPercent });

  renderChart("dashboard-memory-chart", [
    { key: "memoryTotal", label: "总用量", color: "var(--chart-1)" },
    { key: "memorySoftware", label: "软件用量", color: "var(--chart-2)", dash: "6 4" },
    { key: "memoryRetained", label: "保留堆", color: "var(--chart-3)", dash: "1 3" },
  ], { min: 0, formatter: formatBytes });

  renderChart("dashboard-speed-chart", [
    { key: "gotdSpeed", label: "gotd", color: "var(--chart-1)" },
  ], { min: 0, formatter: speedText });
  renderDashboardHistory();
}

function renderDashboardHistory() {
  if (!document.getElementById("dashboard-history")?.open) return;
  const rows = state.dashboardSamples.slice().reverse().map(sample => {
    const values = [sampleClock.format(sample.at), speedText(sample.gotdSpeed), reading(sample.cpu, formatPercent),
      reading(sample.memoryTotal, formatBytes), reading(sample.memorySoftware, formatBytes), formatBytes(sample.memoryRetained)];
    return `<tr>${values.map(value => `<td>${escapeHTML(value)}</td>`).join("")}</tr>`;
  });
  document.getElementById("dashboard-history-body").innerHTML = rows.join("") || '<tr><td colspan="6">尚未收到采样</td></tr>';
}

function setErrorCount(id, count) {
  const element = document.getElementById(id);
  element.textContent = reading(count, formatCount);
  element.toggleAttribute("data-attention", count > 0);
}

function formatDCSchedulers(schedulers) {
  if (!Array.isArray(schedulers) || schedulers.length === 0) return "DC 调度器：暂无活动";
  return schedulers.map((item) => {
    const dc = formatCount(item.dc);
    const capacity = formatCount(item.capacity);
    const chunks = formatCount(item.active_chunks);
    const files = formatCount(item.active_files);
    const queued = formatCount(item.queued_requests);
    return `DC${dc}：许可 ${chunks}/${capacity} · 文件 ${files} · 排队 ${queued}`;
  }).join("；");
}

function renderChart(svgID, series, options = {}) {
  const svg = document.getElementById(svgID);
  if (!svg) return;

  const bounds = svg.getBoundingClientRect();
  const width = Math.max(1, Math.round(bounds.width || 640));
  const height = Math.max(1, Math.round(bounds.height || 160));
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.removeAttribute("preserveAspectRatio");

  const range = dashboardChartRange(series, options);
  const formatter = options.formatter || String;
  const axisValues = [range.max, (range.min + range.max) / 2, range.min];
  const labelWidth = Math.max(...axisValues.map(value => estimatedTextWidth(formatter(value))));
  const padding = { top: 10, right: 6, bottom: 22, left: Math.ceil(labelWidth) + 10 };
  const plotWidth = width - padding.left - padding.right;
  const plotHeight = height - padding.top - padding.bottom;
  const samples = state.dashboardSamples;
  const start = samples.at(-1).at - historyWindow;

  const yFor = (value) => {
    const ratio = (Number(value || 0) - range.min) / (range.max - range.min);
    return padding.top + (1 - Math.min(1, Math.max(0, ratio))) * plotHeight;
  };
  const xFor = (index) => padding.left + (samples[index].at - start) / historyWindow * plotWidth;

  const grid = [0, 0.5].map((ratio) => {
    const y = padding.top + ratio * plotHeight;
    return `<line class="grid-line" x1="${padding.left}" y1="${y.toFixed(2)}" x2="${(width - padding.right).toFixed(2)}" y2="${y.toFixed(2)}"></line>`;
  }).join("");

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
    `<text class="axis-label" x="${padding.left}" y="${(height - 5).toFixed(2)}" text-anchor="start">30 秒前</text>`,
    `<text class="axis-label" x="${(width - padding.right).toFixed(2)}" y="${(height - 5).toFixed(2)}" text-anchor="end">最新采样</text>`,
  ].join("");

  const paths = series.map((item) => {
    let markers = "";
    const d = samples.map((sample, index) => {
      if (sample[item.key] == null) return "";
      const x = xFor(index).toFixed(2);
      const y = yFor(sample[item.key]).toFixed(2);
      const first = samples[index - 1]?.[item.key] == null;
      if (first && samples[index + 1]?.[item.key] == null) {
        markers += `<circle cx="${x}" cy="${y}" r="3" fill="${item.color}"></circle>`;
      }
      return `${first ? "M" : "L"}${x} ${y}`;
    }).join(" ");
    return `<path class="series-line" d="${d}" stroke="${item.color}" stroke-dasharray="${item.dash || "none"}"></path>${markers}`;
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
    start,
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

  function showPoint(clientX) {
    const rect = svg.getBoundingClientRect();
    const rawX = (clientX - rect.left) * chart.width / rect.width;
    const x = Math.min(chart.width - chart.padding.right, Math.max(chart.padding.left, rawX));
    const time = chart.start + (x - chart.padding.left) / chart.plotWidth * historyWindow;
    const index = state.dashboardSamples.reduce((nearest, sample, i, samples) =>
      Math.abs(sample.at - time) < Math.abs(samples[nearest].at - time) ? i : nearest, 0);
    renderDashboardChartHover(hoverLayer, chart, index);
  }
  svg.onpointermove = event => {
    chartPointers.set(svg, event.clientX);
    showPoint(event.clientX);
  };
  svg.onpointerleave = svg.onpointercancel = () => {
    chartPointers.delete(svg);
    hoverLayer.innerHTML = "";
  };
  if (chartPointers.has(svg)) showPoint(chartPointers.get(svg));
}

function renderDashboardChartHover(layer, chart, index) {
  const sample = state.dashboardSamples[index];
  if (!sample) return;

  const pointX = chart.xFor(index);
  const rows = chart.series.map((item) => ({
    color: item.color,
    label: item.label || item.key,
    value: reading(sample[item.key], chart.formatter),
    y: sample[item.key] == null ? null : chart.yFor(sample[item.key]),
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

  const points = rows.filter(row => row.y != null).map((row) => (
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
  return seconds === 0 ? "最新采样" : `${seconds} 秒前`;
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
  const max = niceCeil(Math.max(maxValue * 1.15, min + 2));
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

function setDashboardStatus(message, kind = "") {
  const status = document.getElementById("dashboard-status");
  if (!status) return;
  status.className = `notice ${kind}`.trim();
  if (status.textContent !== message) status.textContent = message;
}

export const page = { init: initDashboard, load: loadDashboard, stop: stopDashboardPolling };

function stopOverview() {
  ++overviewGeneration;
  for (const release of overviewReleases) release();
  overviewReleases = [];
}

async function loadOverview() {
  stopOverview();
  const generation = overviewGeneration;
  void observeOverview("forwards", "/api/forwards", generation, receiveForwardOverview);
  try {
    const downloader = await currentDownloader();
    if (generation !== overviewGeneration) return;
    const mode = trackedExecutor(downloader);
    if (!mode) {
      showDownloadCounts(null, "未启用任务下载器");
      return;
    }
    const label = mode === "local" ? "本地" : "aria2";
    await observeOverview(`download-tasks-${mode}`, `/api/download-tasks?executor=${mode}`, generation, (data, error) =>
      showDownloadCounts(error ? null : data.items || [], error ? `${label}下载器不可用` : `${label} · 未结束`));
  } catch {
    if (generation === overviewGeneration) showDownloadCounts(null, "下载配置不可用");
  }
}

async function observeOverview(topic, endpoint, generation, receive) {
  let eventReceived = false;
  overviewReleases.push(observe(topic, (data, error) => {
    if (generation !== overviewGeneration) return;
    eventReceived = true;
    receive(data, error);
  }));
  try {
    const data = await api(endpoint, { signal: AbortSignal.timeout(5000) });
    if (generation === overviewGeneration && !eventReceived) receive(data);
  } catch (error) {
    if (generation === overviewGeneration && !eventReceived) receive(null, error.message);
  }
}

function showDownloadCounts(items, note) {
  const active = items?.filter(task => !["complete", "removed"].includes(task.state)).length;
  const errors = items?.filter(task => task.state === "error").length;
  setText("overview-downloads", reading(active, formatCount));
  setText("overview-downloads-note", note);
  setErrorCount("overview-download-errors", errors);
  setText("overview-download-errors-note", items ? (errors ? "查看失败任务" : "无失败任务") : note);
}

function receiveForwardOverview(data, error) {
  const items = error ? null : data.items || [];
  const active = items?.filter(task => !["done", "error"].includes(task.status)).length;
  const errors = items?.filter(task => task.status === "error").length;
  setText("overview-forwards", reading(active, formatCount));
  setText("overview-forwards-note", error ? "转发服务不可用" : "当前账号未结束任务");
  setErrorCount("overview-forward-errors", errors);
  setText("overview-forward-errors-note", error ? "转发服务不可用" : errors ? "查看失败任务" : "无失败任务");
}

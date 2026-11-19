// Shared mutable application state. Imported as a single object reference by
// every page module, so property mutations are visible everywhere.
export const state = {
  config: null,
  aria2Loaded: false,
  downloaderMode: "aria2",
  downloader: null,
  localDownloads: [],
  selectedLocalDownloads: new Set(),
  localDownloadSamples: new Map(),
  localDownloadPoll: null,
  localDownloadLoading: false,
  forwardTasks: [],
  selectedForwards: new Set(),
  forwardPoll: null,
  forwardLoading: false,
  dashboardPoll: null,
  dashboardLoading: false,
  dashboardSamples: [],
  kvItems: [],
  selectedKV: new Set(),
  kvSort: { field: "created_at", dir: "desc" },
  loginPoll: null,
  update: null,
  modules: [],
  userSessions: [],
  currentNamespace: "",
  loginMethod: "phone",
  loginPanel: "",
  lastLoginData: null,
};

export function trackedExecutor(downloader) {
  if (downloader?.mode === "local") return "local";
  if (downloader?.mode === "aria2" && downloader.aria2_enabled !== false) return "aria2";
  return "";
}

package webui

import (
	"net/http"
	"runtime"
	"time"

	hostmem "github.com/shirou/gopsutil/v3/mem"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/ps"
)

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	cfg := config.Get()
	now := time.Now()
	metricErrors := map[string]string{}

	cpuPercent, err := ps.GetSelfCPU(r.Context())
	if err != nil {
		metricErrors["cpu"] = err.Error()
	}

	var rss uint64
	if memInfo, err := ps.GetSelfMem(r.Context()); err != nil {
		metricErrors["memory"] = err.Error()
	} else if memInfo != nil {
		rss = memInfo.RSS
	}
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	retainedIdleBytes := memStats.HeapIdle
	if retainedIdleBytes >= memStats.HeapReleased {
		retainedIdleBytes -= memStats.HeapReleased
	} else {
		retainedIdleBytes = 0
	}

	var memoryTotal uint64
	var memoryPercent float64
	if vm, err := hostmem.VirtualMemoryWithContext(r.Context()); err != nil {
		metricErrors["memory_total"] = err.Error()
	} else if vm != nil && vm.Total > 0 {
		memoryTotal = vm.Total
		memoryPercent = float64(rss) / float64(vm.Total) * 100
	}

	bufferBytes := uint64(httpdl.HTTPBufferBytes())
	var softwareBytes uint64
	if bufferBytes <= rss {
		softwareBytes = rss - bufferBytes
	} else {
		softwareBytes = 0
	}
	if retainedIdleBytes <= softwareBytes {
		softwareBytes -= retainedIdleBytes
	} else {
		softwareBytes = 0
	}

	totalBytes := httpdl.TelegramDownloadedBytes()
	gotdSpeed := s.telegramDownloadSpeed(totalBytes, now)
	activeChunkRequests := httpdl.ActiveTelegramFileRequests()
	telegramFileErrors := httpdl.TelegramFileErrorCount()
	telegramFileErrors10s := httpdl.TelegramFileErrorCountSince(10 * time.Second)
	var aria2Stat aria2DashboardStat
	if cfg != nil {
		stat, err := fetchAria2DashboardStat(r.Context(), cfg.Aria2)
		aria2Stat = stat
		if err != nil {
			metricErrors["aria2"] = err.Error()
		}
	}

	response := map[string]any{
		"sampled_at": now.UTC().Format(time.RFC3339Nano),
		"process": map[string]any{
			"cpu_percent": cpuPercent,
			"memory_rss":  rss,
			"goroutines":  ps.GetGoroutineNum(),
		},
		"memory": map[string]any{
			"total_bytes":              rss,
			"software_bytes":           softwareBytes,
			"buffer_bytes":             bufferBytes,
			"heap_alloc_bytes":         memStats.Alloc,
			"heap_sys_bytes":           memStats.HeapSys,
			"heap_idle_bytes":          memStats.HeapIdle,
			"heap_released_bytes":      memStats.HeapReleased,
			"heap_retained_idle_bytes": retainedIdleBytes,
			"system_total":             memoryTotal,
			"total_percent":            memoryPercent,
		},
		"download": map[string]any{
			"gotd_bytes_total":         totalBytes,
			"gotd_speed_bps":           gotdSpeed,
			"aria2_speed_bps":          aria2Stat.DownloadSpeedBPS,
			"aria2_available":          aria2Stat.Available,
			"active_chunk_requests":    activeChunkRequests,
			"telegram_file_errors":     telegramFileErrors,
			"telegram_file_errors_10s": telegramFileErrors10s,
			"aria2_task_count":         aria2Stat.TotalTasks(),
			"aria2_active_tasks":       aria2Stat.ActiveTasks,
			"aria2_waiting_tasks":      aria2Stat.WaitingTasks,
			"aria2_stopped_tasks":      aria2Stat.StoppedTasks,
		},
		"http": map[string]any{
			"active_chunk_requests":    activeChunkRequests,
			"telegram_file_errors":     telegramFileErrors,
			"telegram_file_errors_10s": telegramFileErrors10s,
		},
		"aria2": map[string]any{
			"available":     aria2Stat.Available,
			"speed_bps":     aria2Stat.DownloadSpeedBPS,
			"task_count":    aria2Stat.TotalTasks(),
			"active_tasks":  aria2Stat.ActiveTasks,
			"waiting_tasks": aria2Stat.WaitingTasks,
			"stopped_tasks": aria2Stat.StoppedTasks,
		},
	}
	if len(metricErrors) > 0 {
		response["errors"] = metricErrors
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.Get()
	writeJSON(w, http.StatusOK, map[string]any{
		fieldNamespace:  s.namespace(),
		"watch_running": s.watchRunning(),
		"webui": map[string]any{
			"listen":                     config.WebUIListenAddr(cfg),
			"address":                    cfg.WebUI.Address,
			"port":                       cfg.WebUI.Port,
			"user":                       cfg.WebUI.Username,
			fieldUsingDefaultCredentials: config.UsesDefaultWebUICredentials(cfg),
		},
		"aria2": map[string]any{
			"rpc_url": cfg.Aria2.RPCURL,
			"proxy":   "/aria2/jsonrpc",
		},
		"downloader": map[string]any{
			"mode": config.EffectiveDownloaderMode(cfg),
		},
		"http": map[string]any{
			"listen":          config.HTTPListenAddr(cfg),
			"address":         cfg.HTTP.Address,
			"port":            cfg.HTTP.Port,
			"public_base_url": cfg.HTTP.PublicBaseURL,
			"download_ttl":    cfg.HTTP.DownloadLinkTTLHours,
		},
		"version": versionInfo(),
	})
}

func (s *Server) telegramDownloadSpeed(totalBytes int64, sampledAt time.Time) float64 {
	s.dashboardMu.Lock()
	defer s.dashboardMu.Unlock()

	if s.dashboardLastSample.IsZero() {
		s.dashboardLastBytes = totalBytes
		s.dashboardLastSample = sampledAt
		return 0
	}

	elapsed := sampledAt.Sub(s.dashboardLastSample).Seconds()
	delta := totalBytes - s.dashboardLastBytes
	s.dashboardLastBytes = totalBytes
	s.dashboardLastSample = sampledAt
	if elapsed <= 0 || delta <= 0 {
		return 0
	}
	return float64(delta) / elapsed
}

func versionInfo() map[string]any {
	return map[string]any{
		"version": consts.Version,
		"commit":  consts.Commit,
		"date":    consts.CommitDate,
	}
}

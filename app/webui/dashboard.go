package webui

import (
	"context"
	"net/http"
	"runtime"
	"time"

	hostmem "github.com/shirou/gopsutil/v3/mem"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/ps"
	"github.com/snakexgc/tdl/rte"
)

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"time": rte.Now(s.opts.Context).UTC().Format(time.RFC3339Nano),
	})
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}

	data, err := s.samples.Read(r.Context(), "dashboard", s.dashboardSnapshot)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) dashboardSnapshot(ctx context.Context) (any, error) {
	now := time.Now()
	metricErrors := map[string]string{}

	cpuPercent, err := ps.GetSelfCPU(ctx)
	if err != nil {
		metricErrors["cpu"] = err.Error()
	}

	var rss uint64
	if memInfo, err := ps.GetSelfMem(ctx); err != nil {
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
	if vm, err := hostmem.VirtualMemoryWithContext(ctx); err != nil {
		metricErrors["memory_total"] = err.Error()
	} else if vm != nil && vm.Total > 0 {
		memoryTotal = vm.Total
		memoryPercent = float64(rss) / float64(vm.Total) * 100
	}

	softwareBytes := rss
	if retainedIdleBytes <= softwareBytes {
		softwareBytes -= retainedIdleBytes
	} else {
		softwareBytes = 0
	}

	totalBytes := httpdl.TelegramDownloadedBytes()
	gotdSpeed := s.telegramDownloadSpeed(totalBytes, now)
	activeChunkRequests := httpdl.ActiveTelegramFileRequests()
	dcSchedulers := httpdl.DCSchedulerSnapshots()
	telegramFileErrors := httpdl.TelegramFileErrorCount()
	telegramFileErrors10s := httpdl.TelegramFileErrorCountSince(10 * time.Second)

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
			"active_chunk_requests":    activeChunkRequests,
			"telegram_file_errors":     telegramFileErrors,
			"telegram_file_errors_10s": telegramFileErrors10s,
		},
		"http": map[string]any{
			"active_chunk_requests":    activeChunkRequests,
			"telegram_file_errors":     telegramFileErrors,
			"telegram_file_errors_10s": telegramFileErrors10s,
			"dc_schedulers":            dcSchedulers,
		},
	}
	if len(metricErrors) > 0 {
		response["errors"] = metricErrors
	}
	return response, nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, s.statusSnapshot())
}

func (s *Server) statusSnapshot() map[string]any {
	cfg := config.From(s.opts.Context)
	var configurationVersion uint64
	if versioned, ok := s.opts.ComponentManager.(interface{ ConfigurationVersion() uint64 }); ok {
		configurationVersion = versioned.ConfigurationVersion()
	}
	return map[string]any{
		"clock":                 rte.ClockFrom(s.opts.Context).Status(),
		"configuration_version": configurationVersion,
		fieldNamespace:          s.namespace(),
		"watch_running":         s.watchRunning(),
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
			"mode": config.PrimaryDownloadExecutor(cfg),
		},
		"http": map[string]any{
			"listen":          config.HTTPListenAddr(cfg),
			"address":         cfg.HTTP.Address,
			"port":            cfg.HTTP.Port,
			"public_base_url": cfg.HTTP.PublicBaseURL,
			"download_ttl":    cfg.HTTP.DownloadLinkTTLHours,
		},
		"version": versionInfo(),
	}
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

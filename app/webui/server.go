package webui

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	hostmem "github.com/shirou/gopsutil/v3/mem"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
	"github.com/iyear/tdl/pkg/kv"
	"github.com/iyear/tdl/pkg/ps"
)

//go:embed index.html login.html aria2ng.html static/* views/*
var assets embed.FS

const (
	downloadTaskKeyPrefix = httpdl.DownloadTaskKeyPrefix
	downloadTaskIndexKey  = httpdl.DownloadTaskIndexKey
	aria2TaskKeyPrefix    = "watch.aria2.task."
	aria2TaskIndexKey     = "watch.aria2.index"

	aria2StatusComplete    = "complete"
	tdlAria2PieceSize      = "1024K"
	tdlAria2TimeoutSeconds = "600"

	userSessionKey = "session"
	userAppKey     = "app"

	webUICookieName = "tdl_webui_session"
	webUISessionTTL = 24 * time.Hour

	fieldUsingDefaultCredentials = "using_default_credentials"
	fieldNamespace               = "namespace"
	fieldDeleted                 = "deleted"
	valueTrue                    = "true"
	fieldMessage                 = "message"
	fieldDefault                 = "default"
)

type Options struct {
	Context         context.Context
	KVEngine        kv.Storage
	Namespace       string
	NamespaceKV     storage.Storage
	AfterConfigSave func(*config.Config)
	OnLoginSuccess  func(*tg.User)
	RequestReboot   func()
	RequestUpdate   func(updater.Plan)
	WatchRunning    func() bool
	ModuleManager   ModuleManager
}

type ModuleManager interface {
	ModuleStates() []ModuleState
	SetModuleEnabled(ctx context.Context, id string, enabled bool) (ModuleState, error)
}

type ModuleState struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	CanToggle   bool   `json:"can_toggle"`
	Status      string `json:"status"`
}

type Server struct {
	opts Options

	login *webLoginManager

	sessionMu sync.Mutex
	sessions  map[string]time.Time

	dashboardMu         sync.Mutex
	dashboardLastBytes  int64
	dashboardLastSample time.Time
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.Get()
	if cfg == nil || strings.TrimSpace(config.WebUIListenAddr(cfg)) == "" {
		return nil
	}
	if strings.TrimSpace(cfg.WebUI.Username) == "" || cfg.WebUI.Password == "" {
		return nil
	}
	if opts.Context == nil {
		opts.Context = ctx
	}

	server := NewServer(opts)
	server.startAria2SyncLoop(ctx)

	httpServer := &http.Server{
		Addr:    config.WebUIListenAddr(cfg),
		Handler: server.routes(),
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	return httpServer.ListenAndServe()
}

func NewServer(opts Options) *Server {
	return &Server{
		opts:     opts,
		login:    newWebLoginManager(opts),
		sessions: map[string]time.Time{},
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(assets, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	mux.HandleFunc("/login", s.handleLoginPage)
	mux.HandleFunc("/api/auth/session", s.handleAuthSession)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", s.authFunc(s.handleAuthLogout))
	mux.HandleFunc("/views/", s.authFunc(s.handleViewAsset))
	mux.HandleFunc("/aria2ng.html", s.authFunc(s.handleAsset("aria2ng.html", "text/html; charset=utf-8")))
	mux.HandleFunc("/aria2/jsonrpc", s.authFunc(s.handleAria2Proxy))
	mux.HandleFunc("/api/heartbeat", s.authFunc(s.handleHeartbeat))
	mux.HandleFunc("/api/dashboard", s.authFunc(s.handleDashboard))
	mux.HandleFunc("/api/status", s.authFunc(s.handleStatus))
	mux.HandleFunc("/api/aria2/check", s.authFunc(s.handleAria2Check))
	mux.HandleFunc("/api/internal-downloads", s.authFunc(s.handleInternalDownloads))
	mux.HandleFunc("/api/internal-downloads/actions", s.authFunc(s.handleInternalDownloadActions))
	mux.HandleFunc("/api/kv/links", s.authFunc(s.handleKVLinks))
	mux.HandleFunc("/api/kv/links/actions", s.authFunc(s.handleKVActions))
	mux.HandleFunc("/api/kv/links/", s.authFunc(s.handleKVLink))
	mux.HandleFunc("/api/user", s.authFunc(s.handleUser))
	mux.HandleFunc("/api/user/switch", s.authFunc(s.handleUserSwitch))
	mux.HandleFunc("/api/user/delete", s.authFunc(s.handleUserDelete))
	mux.HandleFunc("/api/login/status", s.authFunc(s.handleLoginStatus))
	mux.HandleFunc("/api/login/phone/start", s.authFunc(s.handleLoginPhoneStart))
	mux.HandleFunc("/api/login/code", s.authFunc(s.handleLoginCode))
	mux.HandleFunc("/api/login/password", s.authFunc(s.handleLoginPassword))
	mux.HandleFunc("/api/login/cancel", s.authFunc(s.handleLoginCancel))
	mux.HandleFunc("/api/modules", s.authFunc(s.handleModules))
	mux.HandleFunc("/api/config", s.authFunc(s.handleConfig))
	mux.HandleFunc("/api/update/check", s.authFunc(s.handleUpdateCheck))
	mux.HandleFunc("/api/update/apply", s.authFunc(s.handleUpdateApply))
	mux.HandleFunc("/api/system/reboot", s.authFunc(s.handleReboot))
	mux.HandleFunc("/", s.authFunc(s.handleAsset("index.html", "text/html; charset=utf-8")))

	return mux
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.sessionOK(r) {
			if wantsHTML(r) {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
				return
			}
			writeError(w, http.StatusUnauthorized, errors.New("authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) authFunc(fn http.HandlerFunc) http.HandlerFunc {
	return s.auth(fn).ServeHTTP
}

func credentialsOK(gotUser, gotPassword, username, password string) bool {
	if username == "" || password == "" || gotUser == "" || gotPassword == "" {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(gotUser), []byte(username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(gotPassword), []byte(password)) == 1
	return userOK && passOK
}

func (s *Server) sessionOK(r *http.Request) bool {
	cookie, err := r.Cookie(webUICookieName)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	token := cookie.Value
	now := time.Now()

	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	expiresAt, ok := s.sessions[token]
	if !ok || !expiresAt.After(now) {
		delete(s.sessions, token)
		return false
	}
	s.sessions[token] = now.Add(webUISessionTTL)
	return true
}

func (s *Server) issueSession(w http.ResponseWriter, r *http.Request) error {
	token, err := newSessionToken()
	if err != nil {
		return err
	}
	s.sessionMu.Lock()
	s.sessions[token] = time.Now().Add(webUISessionTTL)
	s.sessionMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     webUICookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int(webUISessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
	return nil
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(webUICookieName); err == nil {
		s.sessionMu.Lock()
		delete(s.sessions, cookie.Value)
		s.sessionMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{
		Name:     webUICookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
	})
}

func newSessionToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", errors.Wrap(err, "generate session token")
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func requestIsHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func wantsHTML(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/views/") || strings.HasPrefix(r.URL.Path, "/aria2/") {
		return false
	}
	accept := r.Header.Get("Accept")
	return r.Method == http.MethodGet && (accept == "" || strings.Contains(accept, "text/html"))
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.sessionOK(r) {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.serveAsset(w, r, "login.html", "text/html; charset=utf-8")
}

func (s *Server) handleAuthSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	if !s.sessionOK(r) {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	cfg := config.Get()
	user := ""
	if cfg != nil {
		user = cfg.WebUI.Username
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"user":          user,
		"webui": map[string]any{
			fieldUsingDefaultCredentials: config.UsesDefaultWebUICredentials(cfg),
		},
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	cfg := config.Get()
	if cfg == nil || !credentialsOK(strings.TrimSpace(req.Username), req.Password, cfg.WebUI.Username, cfg.WebUI.Password) {
		writeError(w, http.StatusUnauthorized, errors.New("用户名或密码错误"))
		return
	}
	if err := s.issueSession(w, r); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                         true,
		fieldUsingDefaultCredentials: config.UsesDefaultWebUICredentials(cfg),
	})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	s.clearSession(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleViewAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/views/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || !strings.HasSuffix(name, ".html") {
		http.NotFound(w, r)
		return
	}
	s.serveAsset(w, r, "views/"+name, "text/html; charset=utf-8")
}

func (s *Server) handleAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if name == "index.html" && r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		s.serveAsset(w, r, name, contentType)
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}

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

type aria2GlobalStat struct {
	DownloadSpeed string `json:"downloadSpeed"`
	NumActive     string `json:"numActive"`
	NumWaiting    string `json:"numWaiting"`
	NumStopped    string `json:"numStopped"`
}

type aria2DashboardStat struct {
	Available        bool
	DownloadSpeedBPS int64
	ActiveTasks      int64
	WaitingTasks     int64
	StoppedTasks     int64
}

func (s aria2DashboardStat) TotalTasks() int64 {
	return s.ActiveTasks + s.WaitingTasks + s.StoppedTasks
}

func fetchAria2DashboardStat(ctx context.Context, cfg config.Aria2Config) (aria2DashboardStat, error) {
	var result aria2DashboardStat
	if strings.TrimSpace(cfg.RPCURL) == "" {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	var stat aria2GlobalStat
	if err := callAria2(ctx, cfg, "aria2.getGlobalStat", []any{}, &stat); err != nil {
		result.Available = true
		return result, err
	}
	result.Available = true
	result.DownloadSpeedBPS = parseAria2Length(stat.DownloadSpeed)
	result.ActiveTasks = parseAria2Length(stat.NumActive)
	result.WaitingTasks = parseAria2Length(stat.NumWaiting)
	result.StoppedTasks = parseAria2Length(stat.NumStopped)
	return result, nil
}

type aria2CheckResult struct {
	OK         bool   `json:"ok"`
	Configured bool   `json:"configured"`
	RPCURL     string `json:"rpc_url"`
	Version    string `json:"version,omitempty"`
	Message    string `json:"message"`
	Error      string `json:"error,omitempty"`
}

func (s *Server) handleAria2Check(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, checkAria2(r.Context(), config.Get().Aria2))
}

func checkAria2(ctx context.Context, cfg config.Aria2Config) aria2CheckResult {
	rpcURL := strings.TrimSpace(cfg.RPCURL)
	result := aria2CheckResult{
		RPCURL: rpcURL,
	}
	if rpcURL == "" {
		result.Message = "尚未配置 aria2.rpc_url。请先在配置设置中填写 aria2 JSON-RPC 地址。"
		return result
	}
	result.Configured = true

	parsed, err := url.Parse(rpcURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		result.Message = "aria2.rpc_url 格式不正确。"
		if err != nil {
			result.Error = err.Error()
		}
		return result
	}

	var version struct {
		Version string `json:"version"`
	}
	if err := callAria2(ctx, cfg, "aria2.getVersion", []any{}, &version); err != nil {
		result.Message = "无法连接 aria2 JSON-RPC。请检查地址、端口、网络和密钥。"
		result.Error = err.Error()
		return result
	}

	result.OK = true
	result.Version = version.Version
	if version.Version != "" {
		result.Message = "aria2 连接正常，版本：" + version.Version
	} else {
		result.Message = "aria2 连接正常。"
	}
	return result
}

func (s *Server) handleInternalDownloads(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	controller := s.internalDownloadController()
	items, err := controller.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items": items,
	})
}

func (s *Server) handleInternalDownloadActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	controller := s.internalDownloadController()
	action := strings.ToLower(strings.TrimSpace(req.Action))
	var (
		result watch.InternalDownloadActionResult
		err    error
	)
	switch action {
	case "pause":
		result, err = controller.Pause(r.Context(), req.IDs)
	case "start":
		result, err = controller.Start(r.Context(), req.IDs)
	case "delete":
		result, err = controller.Delete(r.Context(), req.IDs)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", req.Action))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":     len(result.Errors) == 0,
		"result": result,
	})
}

func versionInfo() map[string]any {
	return map[string]any{
		"version": consts.Version,
		"commit":  consts.Commit,
		"date":    consts.CommitDate,
	}
}

func (s *Server) handleKVLinks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, statusErr, err := s.listDownloadLinks(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items":        items,
			"status_error": statusErr,
		})
	default:
		methodNotAllowed(w, "GET")
	}
}

func (s *Server) handleKVLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		methodNotAllowed(w, "DELETE")
		return
	}
	if s.opts.NamespaceKV == nil {
		writeError(w, http.StatusInternalServerError, errors.New("namespace kv storage is not configured"))
		return
	}
	id, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/api/kv/links/"))
	if err != nil || id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	deleted, err := s.deleteDownloadLink(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldDeleted: deleted})
}

func (s *Server) handleKVActions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Action string   `json:"action"`
		IDs    []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, errors.New("no links selected"))
		return
	}

	switch req.Action {
	case "delete":
		var deleted int
		var itemErrors []string
		for _, id := range req.IDs {
			n, err := s.deleteDownloadLink(r.Context(), id)
			if err != nil {
				itemErrors = append(itemErrors, fmt.Sprintf("%s: %v", id, err))
				continue
			}
			deleted += n
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      len(itemErrors) == 0,
			"deleted": deleted,
			"errors":  itemErrors,
		})
	case "download":
		result := s.downloadLinks(r.Context(), req.IDs)
		writeJSON(w, http.StatusOK, result)
	default:
		writeError(w, http.StatusBadRequest, fmt.Errorf("unsupported action %q", req.Action))
	}
}

type downloadLinkItem struct {
	ID         string              `json:"id"`
	Key        string              `json:"key"`
	URL        string              `json:"url"`
	FileName   string              `json:"file_name"`
	FileSize   int64               `json:"file_size"`
	PeerID     int64               `json:"peer_id"`
	MessageID  int                 `json:"message_id"`
	CreatedAt  time.Time           `json:"created_at"`
	ExpiresAt  *time.Time          `json:"expires_at,omitempty"`
	Permanent  bool                `json:"permanent"`
	Expired    bool                `json:"expired"`
	Downloaded bool                `json:"downloaded"`
	Status     string              `json:"status"`
	Aria2      []aria2LinkEntry    `json:"aria2"`
	Internal   []internalLinkEntry `json:"internal"`
}

type aria2LinkEntry struct {
	GID         string    `json:"gid"`
	Status      string    `json:"status"`
	Downloaded  bool      `json:"downloaded"`
	DownloadURL string    `json:"download_url"`
	Dir         string    `json:"dir"`
	Out         string    `json:"out"`
	CreatedAt   time.Time `json:"created_at"`
	Total       int64     `json:"total"`
	Completed   int64     `json:"completed"`
	Error       string    `json:"error,omitempty"`
}

type internalLinkEntry struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	Path      string    `json:"path"`
	Total     int64     `json:"total"`
	Completed int64     `json:"completed"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type persistentDownloadTask struct {
	ID         string    `json:"id"`
	PeerID     int64     `json:"peer_id"`
	MessageID  int       `json:"message_id"`
	FileName   string    `json:"file_name"`
	FileSize   int64     `json:"file_size"`
	CreatedAt  time.Time `json:"created_at"`
	Downloaded bool      `json:"downloaded"`
}

type aria2TaskRecord struct {
	GID          string    `json:"gid"`
	TaskID       string    `json:"task_id"`
	DownloadURL  string    `json:"download_url"`
	Dir          string    `json:"dir"`
	Out          string    `json:"out"`
	Connections  int       `json:"connections"`
	TransferMode string    `json:"transfer_mode,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Status       string    `json:"status"`
	Total        int64     `json:"total"`
	Completed    int64     `json:"completed"`
	Error        string    `json:"error,omitempty"`
}

type aria2Status struct {
	GID             string      `json:"gid"`
	Status          string      `json:"status"`
	TotalLength     string      `json:"totalLength"`
	CompletedLength string      `json:"completedLength"`
	ErrorCode       string      `json:"errorCode"`
	ErrorMessage    string      `json:"errorMessage"`
	Files           []aria2File `json:"files"`
}

type aria2File struct {
	Length          string     `json:"length"`
	CompletedLength string     `json:"completedLength"`
	Path            string     `json:"path"`
	URIs            []aria2URI `json:"uris"`
}

type aria2URI struct {
	URI string `json:"uri"`
}

func (s *Server) listDownloadLinks(ctx context.Context) ([]downloadLinkItem, string, error) {
	if s.opts.KVEngine == nil {
		return nil, "", errors.New("kv engine is not configured")
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return nil, "", errors.Wrap(err, "list kv keys")
	}
	pairs := meta[s.namespace()]

	records, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return nil, "", err
	}

	cfg := config.Get()
	downloaderMode := config.EffectiveDownloaderMode(cfg)
	statusByGID := map[string]aria2Status{}
	var statusErrText string
	if downloaderMode == config.DownloaderModeAria2 {
		var statusErr error
		statusByGID, statusErr = fetchAria2Statuses(ctx, cfg.Aria2)
		if statusErr != nil {
			statusErrText = statusErr.Error()
		} else {
			s.discoverAria2RecordsFromDownloadLinks(ctx, pairs, records, recordsByTask, statusByGID, cfg)
		}
	}
	internalByTask := map[string][]watch.InternalDownloadInfo{}
	if s.opts.NamespaceKV != nil {
		internalItems, err := s.internalDownloadController().List(ctx)
		if err != nil {
			if statusErrText == "" {
				statusErrText = err.Error()
			}
		} else {
			for _, item := range internalItems {
				taskID := item.TaskID
				if taskID == "" {
					taskID = item.ID
				}
				internalByTask[taskID] = append(internalByTask[taskID], item)
			}
		}
	}

	keys := make([]string, 0, len(pairs))
	for key := range pairs {
		if isDownloadTaskRecordKey(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	now := time.Now()
	items := make([]downloadLinkItem, 0, len(keys))
	for _, key := range keys {
		var task persistentDownloadTask
		if err := json.Unmarshal(pairs[key], &task); err != nil {
			continue
		}
		if task.ID == "" {
			task.ID = strings.TrimPrefix(key, downloadTaskKeyPrefix)
		}

		item := downloadLinkItem{
			ID:         task.ID,
			Key:        key,
			URL:        downloadURL(cfg.HTTP.PublicBaseURL, task.ID),
			FileName:   task.FileName,
			FileSize:   task.FileSize,
			PeerID:     task.PeerID,
			MessageID:  task.MessageID,
			CreatedAt:  task.CreatedAt,
			Downloaded: task.Downloaded,
			Status:     "not_submitted",
		}
		if cfg.HTTP.DownloadLinkTTLHours <= 0 {
			item.Permanent = true
		} else if !task.CreatedAt.IsZero() {
			expiresAt := task.CreatedAt.Add(time.Duration(cfg.HTTP.DownloadLinkTTLHours) * time.Hour)
			item.ExpiresAt = &expiresAt
			item.Expired = !expiresAt.After(now)
		}

		for _, record := range recordsByTask[task.ID] {
			entry := aria2LinkEntry{
				GID:         record.GID,
				Status:      "registered",
				DownloadURL: record.DownloadURL,
				Dir:         record.Dir,
				Out:         record.Out,
				CreatedAt:   record.CreatedAt,
			}
			if record.Status != "" {
				entry.Status = record.Status
				entry.Total = record.Total
				entry.Completed = record.Completed
				entry.Error = record.Error
			}
			if st, ok := statusByGID[record.GID]; ok {
				entry.Status = normalizedAria2Status(st.Status)
				entry.Total, entry.Completed = aria2Lengths(st)
				entry.Error = strings.TrimSpace(strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage))
			}
			entry.Downloaded = entry.Status == aria2StatusComplete && (entry.Total == 0 || entry.Completed >= entry.Total)
			if entry.Downloaded {
				item.Downloaded = true
				s.markDownloadTaskDownloaded(ctx, task.ID)
			}
			item.Status = entry.Status
			item.Aria2 = append(item.Aria2, entry)
		}
		for _, internal := range internalByTask[task.ID] {
			entry := internalLinkEntry{
				ID:        internal.ID,
				Status:    internal.Status,
				Path:      internal.Path,
				Total:     internal.Total,
				Completed: internal.Completed,
				Error:     internal.Error,
				CreatedAt: internal.CreatedAt,
				UpdatedAt: internal.UpdatedAt,
			}
			if entry.Status == watch.InternalDownloadStatusComplete && (entry.Total == 0 || entry.Completed >= entry.Total) {
				item.Downloaded = true
				s.markDownloadTaskDownloaded(ctx, task.ID)
			}
			item.Status = entry.Status
			item.Internal = append(item.Internal, entry)
		}
		items = append(items, item)
	}

	return items, statusErrText, nil
}

func (s *Server) startAria2SyncLoop(ctx context.Context) {
	if s.opts.NamespaceKV == nil || s.opts.KVEngine == nil {
		return
	}
	if config.EffectiveDownloaderMode(config.Get()) != config.DownloaderModeAria2 {
		return
	}
	go func() {
		_ = s.syncAria2Statuses(ctx)
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.syncAria2Statuses(ctx)
			}
		}
	}()
}

func (s *Server) syncAria2Statuses(ctx context.Context) error {
	if s.opts.KVEngine == nil || s.opts.NamespaceKV == nil {
		return nil
	}
	if config.EffectiveDownloaderMode(config.Get()) != config.DownloaderModeAria2 {
		return nil
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return err
	}
	pairs := meta[s.namespace()]

	records, recordsByTask, err := s.parseAria2Records(pairs)
	if err != nil {
		return err
	}

	statusByGID, err := fetchAria2Statuses(ctx, config.Get().Aria2)
	if err != nil {
		return err
	}
	s.discoverAria2RecordsFromDownloadLinks(ctx, pairs, records, recordsByTask, statusByGID, config.Get())

	taskCompleted := map[string]bool{}
	taskHasAria2 := map[string]bool{}

	for taskID, taskRecords := range recordsByTask {
		taskHasAria2[taskID] = true
		for _, record := range taskRecords {
			updated := record
			if st, ok := statusByGID[record.GID]; ok {
				updated.Status = normalizedAria2Status(st.Status)
				updated.Total, updated.Completed = aria2Lengths(st)
				updated.Error = strings.TrimSpace(st.ErrorCode + " " + st.ErrorMessage)
			}

			data, err := json.Marshal(updated)
			if err != nil {
				continue
			}
			_ = s.opts.NamespaceKV.Set(ctx, aria2TaskKeyPrefix+record.GID, data)

			if updated.Status == aria2StatusComplete && (updated.Total == 0 || updated.Completed >= updated.Total) {
				taskCompleted[taskID] = true
			}
		}
	}

	now := time.Now()

	aria2Index := map[string]time.Time{}
	if idxData, err := s.opts.NamespaceKV.Get(ctx, aria2TaskIndexKey); err == nil {
		_ = json.Unmarshal(idxData, &aria2Index)
	}
	for _, record := range records {
		if record.Status != aria2StatusComplete {
			aria2Index[record.GID] = now
		}
	}
	if idxData, err := json.Marshal(aria2Index); err == nil {
		_ = s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, idxData)
	}

	downloadIndex := map[string]time.Time{}
	if idxData, err := s.opts.NamespaceKV.Get(ctx, downloadTaskIndexKey); err == nil {
		_ = json.Unmarshal(idxData, &downloadIndex)
	}
	for taskID := range taskHasAria2 {
		if !taskCompleted[taskID] {
			downloadIndex[taskID] = now
		}
	}
	if idxData, err := json.Marshal(downloadIndex); err == nil {
		_ = s.opts.NamespaceKV.Set(ctx, downloadTaskIndexKey, idxData)
	}

	for taskID, isCompleted := range taskCompleted {
		if !isCompleted {
			continue
		}
		s.markDownloadTaskDownloaded(ctx, taskID)
	}

	ttl := httpdl.LinkTTL(config.Get().HTTP)
	if ttl > 0 && len(downloadIndex) > 0 {
		dlChanged := false
		for taskID, createdAt := range downloadIndex {
			if !createdAt.Add(ttl).Before(now) {
				continue
			}
			_ = s.opts.NamespaceKV.Delete(ctx, downloadTaskKeyPrefix+taskID)
			delete(downloadIndex, taskID)
			dlChanged = true
		}
		if dlChanged {
			if idxData, err := json.Marshal(downloadIndex); err == nil {
				_ = s.opts.NamespaceKV.Set(ctx, downloadTaskIndexKey, idxData)
			}
		}
	}

	arIndexChanged := false
	for gid, createdAt := range aria2Index {
		if ttl > 0 && createdAt.Add(ttl).Before(now) {
			_ = s.opts.NamespaceKV.Delete(ctx, aria2TaskKeyPrefix+gid)
			delete(aria2Index, gid)
			arIndexChanged = true
			continue
		}
		if record, ok := records[aria2TaskKeyPrefix+gid]; ok && record.TaskID != "" {
			if _, exists := downloadIndex[record.TaskID]; !exists {
				_ = s.opts.NamespaceKV.Delete(ctx, aria2TaskKeyPrefix+gid)
				delete(aria2Index, gid)
				arIndexChanged = true
			}
		}
	}
	if arIndexChanged {
		if idxData, err := json.Marshal(aria2Index); err == nil {
			_ = s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, idxData)
		}
	}

	return nil
}

func (s *Server) loadAria2Records() (map[string]aria2TaskRecord, map[string][]aria2TaskRecord, error) {
	if s.opts.KVEngine == nil {
		return nil, nil, errors.New("kv engine is not configured")
	}
	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		return nil, nil, errors.Wrap(err, "list kv keys")
	}
	return s.parseAria2Records(meta[s.namespace()])
}

func (s *Server) parseAria2Records(pairs map[string][]byte) (map[string]aria2TaskRecord, map[string][]aria2TaskRecord, error) {
	records := map[string]aria2TaskRecord{}
	byTask := map[string][]aria2TaskRecord{}
	for key, data := range pairs {
		if !strings.HasPrefix(key, aria2TaskKeyPrefix) || key == aria2TaskIndexKey {
			continue
		}
		var record aria2TaskRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return nil, nil, errors.Wrapf(err, "decode %s", key)
		}
		if record.GID == "" {
			record.GID = strings.TrimPrefix(key, aria2TaskKeyPrefix)
		}
		records[key] = record
		if record.TaskID != "" {
			byTask[record.TaskID] = append(byTask[record.TaskID], record)
		}
	}
	for taskID := range byTask {
		sort.SliceStable(byTask[taskID], func(i, j int) bool {
			return byTask[taskID][i].CreatedAt.Before(byTask[taskID][j].CreatedAt)
		})
	}
	return records, byTask, nil
}

type downloadLinkTarget struct {
	TaskID      string
	DownloadURL string
	FileName    string
}

func (s *Server) discoverAria2RecordsFromDownloadLinks(ctx context.Context, pairs map[string][]byte, records map[string]aria2TaskRecord, byTask map[string][]aria2TaskRecord, statusByGID map[string]aria2Status, cfg *config.Config) {
	if len(statusByGID) == 0 {
		return
	}
	if cfg == nil {
		cfg = config.Get()
	}

	publicBaseURL := ""
	threads := config.EffectiveThreads(cfg)
	connections := 1
	transferMode := config.HTTPTransferModeSourceParallel
	if cfg != nil {
		publicBaseURL = cfg.HTTP.PublicBaseURL
		connections = config.HTTPRangeConnectionsFor(cfg.HTTP, threads)
		transferMode = config.EffectiveHTTPTransferMode(cfg)
	}
	targetsByID, targetsByURL := downloadLinkTargets(pairs, cfg)
	if len(targetsByID) == 0 {
		return
	}

	changedTasks := map[string]struct{}{}
	for _, status := range statusByGID {
		if status.GID == "" {
			continue
		}
		key := aria2TaskKeyPrefix + status.GID
		if _, exists := records[key]; exists {
			continue
		}
		target, downloadURL, ok := aria2StatusDownloadTarget(status, targetsByID, targetsByURL, publicBaseURL)
		if !ok {
			continue
		}

		dir, out := aria2StatusPathOptions(status)
		if out == "" {
			out = target.FileName
		}
		total, completed := aria2Lengths(status)
		record := aria2TaskRecord{
			GID:          status.GID,
			TaskID:       target.TaskID,
			DownloadURL:  downloadURL,
			Dir:          dir,
			Out:          out,
			Connections:  connections,
			TransferMode: transferMode,
			CreatedAt:    time.Now(),
			Status:       normalizedAria2Status(status.Status),
			Total:        total,
			Completed:    completed,
			Error:        strings.TrimSpace(status.ErrorCode + " " + status.ErrorMessage),
		}

		records[key] = record
		byTask[target.TaskID] = append(byTask[target.TaskID], record)
		changedTasks[target.TaskID] = struct{}{}
		if s.opts.NamespaceKV != nil {
			_ = s.saveAria2Record(ctx, record)
		}
	}

	for taskID := range changedTasks {
		sort.SliceStable(byTask[taskID], func(i, j int) bool {
			return byTask[taskID][i].CreatedAt.Before(byTask[taskID][j].CreatedAt)
		})
	}
}

func downloadLinkTargets(pairs map[string][]byte, cfg *config.Config) (map[string]downloadLinkTarget, map[string]downloadLinkTarget) {
	byID := map[string]downloadLinkTarget{}
	byURL := map[string]downloadLinkTarget{}
	publicBaseURL := ""
	if cfg != nil {
		publicBaseURL = cfg.HTTP.PublicBaseURL
	}

	for key, data := range pairs {
		if !isDownloadTaskRecordKey(key) {
			continue
		}
		var task persistentDownloadTask
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		if task.ID == "" {
			task.ID = strings.TrimPrefix(key, downloadTaskKeyPrefix)
		}
		if task.ID == "" {
			continue
		}
		target := downloadLinkTarget{
			TaskID:      task.ID,
			DownloadURL: downloadURL(publicBaseURL, task.ID),
			FileName:    task.FileName,
		}
		byID[target.TaskID] = target
		byURL[target.DownloadURL] = target
	}
	return byID, byURL
}

func aria2StatusDownloadTarget(status aria2Status, targetsByID, targetsByURL map[string]downloadLinkTarget, publicBaseURL string) (downloadLinkTarget, string, bool) {
	for _, file := range status.Files {
		for _, uri := range file.URIs {
			raw := strings.TrimSpace(uri.URI)
			if raw == "" {
				continue
			}
			if target, ok := targetsByURL[raw]; ok {
				return target, raw, true
			}
			id := downloadTaskIDFromURL(raw, publicBaseURL)
			if id == "" {
				continue
			}
			if target, ok := targetsByID[id]; ok {
				return target, raw, true
			}
		}
	}
	return downloadLinkTarget{}, "", false
}

func downloadTaskIDFromURL(raw, publicBaseURL string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	expectedPath := "/download/"
	var base *url.URL
	if publicBaseURL != "" {
		if parsed, err := url.Parse(publicBaseURL); err == nil {
			base = parsed
			expectedPath = strings.TrimRight(parsed.Path, "/") + "/download/"
		}
	}

	if base != nil && base.Host != "" && u.Host != "" {
		if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
			return ""
		}
	}
	if !strings.HasPrefix(u.Path, expectedPath) {
		return ""
	}

	id := strings.TrimPrefix(u.Path, expectedPath)
	if idx := strings.Index(id, "/"); idx >= 0 {
		id = id[:idx]
	}
	id, err = url.PathUnescape(id)
	if err != nil || id == "" || strings.Contains(id, "/") || !isDownloadTaskRecordKey(downloadTaskKeyPrefix+id) {
		return ""
	}
	return id
}

func aria2StatusPathOptions(status aria2Status) (dir, out string) {
	if len(status.Files) == 0 || status.Files[0].Path == "" {
		return "", ""
	}
	path := filepath.Clean(status.Files[0].Path)
	if path == "." {
		return "", ""
	}
	return filepath.Dir(path), filepath.Base(path)
}

func (s *Server) markDownloadTaskDownloaded(ctx context.Context, taskID string) {
	if s.opts.NamespaceKV == nil || taskID == "" {
		return
	}
	data, err := s.opts.NamespaceKV.Get(ctx, downloadTaskKeyPrefix+taskID)
	if err != nil {
		return
	}
	data, changed, err := markDownloadTaskDataDownloaded(data, taskID)
	if err != nil || !changed {
		return
	}
	_ = s.opts.NamespaceKV.Set(ctx, downloadTaskKeyPrefix+taskID, data)
}

func markDownloadTaskDataDownloaded(data []byte, taskID string) ([]byte, bool, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, false, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if downloadedRaw, ok := raw["downloaded"]; ok {
		var downloaded bool
		if err := json.Unmarshal(downloadedRaw, &downloaded); err == nil && downloaded {
			return data, false, nil
		}
	}

	raw["downloaded"] = json.RawMessage(valueTrue)
	if idRaw, ok := raw["id"]; !ok || string(idRaw) == `""` || strings.TrimSpace(string(idRaw)) == "" {
		idData, err := json.Marshal(taskID)
		if err != nil {
			return nil, false, err
		}
		raw["id"] = idData
	}
	updated, err := json.Marshal(raw)
	if err != nil {
		return nil, false, err
	}
	return updated, true, nil
}

func hasPersistentDownloadMedia(data []byte) bool {
	var raw struct {
		Media struct {
			Location struct {
				Kind string `json:"kind"`
			} `json:"location"`
		} `json:"media"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	return strings.TrimSpace(raw.Media.Location.Kind) != ""
}

func internalDownloadMetadataError(id string) string {
	return fmt.Sprintf("%s: 下载链接缺少媒体定位信息，无法加入内部下载队列；请删除该 KV 记录后重新触发表情生成下载链接", id)
}

func isRestorePersistentDownloadTaskError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "restore persistent download task")
}

func appendInternalDownloadError(errorsList []string, id string, data []byte, err error) []string {
	if isRestorePersistentDownloadTaskError(err) && !hasPersistentDownloadMedia(data) {
		return append(errorsList, internalDownloadMetadataError(id))
	}
	return append(errorsList, fmt.Sprintf("%s: %v", id, err))
}

func (s *Server) deleteDownloadLink(ctx context.Context, id string) (int, error) {
	if s.opts.NamespaceKV == nil {
		return 0, errors.New("namespace kv storage is not configured")
	}
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(id, "/") || !isDownloadTaskRecordKey(downloadTaskKeyPrefix+id) {
		return 0, errors.New("invalid download link id")
	}

	deleted := 0
	if err := s.opts.NamespaceKV.Delete(ctx, downloadTaskKeyPrefix+id); err != nil {
		return deleted, errors.Wrap(err, "delete download task")
	}
	deleted++

	records, _, err := s.loadAria2Records()
	if err != nil {
		return deleted, nil
	}
	for key, record := range records {
		if record.TaskID != id {
			continue
		}
		if err := s.opts.NamespaceKV.Delete(ctx, key); err != nil {
			return deleted, errors.Wrap(err, "delete aria2 task record")
		}
		deleted++
	}
	result, err := s.internalDownloadController().Delete(ctx, []string{id})
	if err == nil {
		deleted += result.Changed
	}
	return deleted, nil
}

type kvDownloadActionResult struct {
	OK      bool     `json:"ok"`
	Added   int      `json:"added"`
	Skipped int      `json:"skipped"`
	Errors  []string `json:"errors"`
}

func (s *Server) downloadLinks(ctx context.Context, ids []string) kvDownloadActionResult {
	result := kvDownloadActionResult{OK: true}
	if s.opts.NamespaceKV == nil || s.opts.KVEngine == nil {
		result.OK = false
		result.Errors = append(result.Errors, "kv storage is not configured")
		return result
	}

	meta, err := s.opts.KVEngine.MigrateTo()
	if err != nil {
		result.OK = false
		result.Errors = append(result.Errors, err.Error())
		return result
	}
	pairs := meta[s.namespace()]
	cfg := config.Get()
	threads := config.EffectiveThreads(cfg)
	limit := config.EffectiveLimit(cfg)
	connections := config.HTTPRangeConnectionsFor(cfg.HTTP, threads)
	downloaderMode := config.EffectiveDownloaderMode(cfg)
	internalController := s.internalDownloadController()
	aria2Configured := false

	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !isDownloadTaskRecordKey(downloadTaskKeyPrefix + id) {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: reserved download metadata key", id))
			continue
		}
		var task persistentDownloadTask
		data, ok := pairs[downloadTaskKeyPrefix+id]
		if !ok {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: link record not found", id))
			continue
		}
		if err := json.Unmarshal(data, &task); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if task.ID == "" {
			task.ID = id
		}
		if downloaderMode == config.DownloaderModeInternal {
			if _, err := internalController.AddLink(ctx, cfg, task.ID); err != nil {
				result.Skipped++
				result.Errors = appendInternalDownloadError(result.Errors, id, data, err)
				continue
			}
			result.Added++
			continue
		}
		if !aria2Configured {
			if err := configureAria2MaxConcurrentDownloads(ctx, cfg.Aria2, limit); err != nil {
				result.Skipped++
				result.Errors = append(result.Errors, fmt.Sprintf("%s: configure aria2 max concurrent downloads: %v", id, err))
				continue
			}
			aria2Configured = true
		}
		link := downloadURL(cfg.HTTP.PublicBaseURL, task.ID)
		gid, err := addAria2URI(ctx, cfg.Aria2, link, task.FileName, connections)
		if err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if err := s.saveAria2Record(ctx, aria2TaskRecord{
			GID:          gid,
			TaskID:       task.ID,
			DownloadURL:  link,
			Dir:          cfg.Aria2.Dir,
			Out:          task.FileName,
			Connections:  connections,
			TransferMode: config.EffectiveHTTPTransferMode(cfg),
			CreatedAt:    time.Now(),
		}); err != nil {
			result.Skipped++
			result.Errors = append(result.Errors, fmt.Sprintf("%s: persist aria2 record: %v", id, err))
			continue
		}
		result.Added++
	}
	result.OK = len(result.Errors) == 0
	return result
}

func addAria2URI(ctx context.Context, cfg config.Aria2Config, uri, out string, connections int) (string, error) {
	options := map[string]any{
		"continue":                  valueTrue,
		"allow-piece-length-change": valueTrue,
		"allow-overwrite":           valueTrue,
		"auto-file-renaming":        "false",
		"user-agent":                "tdl-webui-aria2",
	}
	applyTDLAria2HTTPConnectionOptions(options, connections)
	if cfg.Dir != "" {
		options["dir"] = cfg.Dir
	}
	if out != "" {
		options["out"] = out
	}
	var gid string
	if err := callAria2(ctx, cfg, "aria2.addUri", []any{[]string{uri}, options}, &gid); err != nil {
		return "", err
	}
	if gid == "" {
		return "", errors.New("aria2 returned empty gid")
	}
	return gid, nil
}

func applyTDLAria2HTTPConnectionOptions(options map[string]any, connections int) {
	if connections < 1 {
		connections = 1
	}
	value := strconv.Itoa(connections)
	options["split"] = value
	options["max-connection-per-server"] = value
	options["min-split-size"] = tdlAria2PieceSize
	options["piece-length"] = tdlAria2PieceSize
	options["timeout"] = tdlAria2TimeoutSeconds
}

func configureAria2MaxConcurrentDownloads(ctx context.Context, cfg config.Aria2Config, limit int) error {
	if limit < 1 {
		return errors.New("limit must be greater than 0")
	}
	var result string
	if err := callAria2(ctx, cfg, "aria2.changeGlobalOption", []any{
		map[string]any{
			"max-concurrent-downloads": strconv.Itoa(limit),
		},
	}, &result); err != nil {
		return err
	}
	if result != "OK" {
		return fmt.Errorf("unexpected aria2 response %q", result)
	}
	return nil
}

func (s *Server) saveAria2Record(ctx context.Context, record aria2TaskRecord) error {
	if s.opts.NamespaceKV == nil {
		return errors.New("namespace kv storage is not configured")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task record")
	}
	if err := s.opts.NamespaceKV.Set(ctx, aria2TaskKeyPrefix+record.GID, data); err != nil {
		return errors.Wrap(err, "save aria2 task record")
	}

	index := map[string]time.Time{}
	indexData, err := s.opts.NamespaceKV.Get(ctx, aria2TaskIndexKey)
	if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return errors.Wrap(err, "load aria2 task index")
	}
	if len(indexData) > 0 {
		if err := json.Unmarshal(indexData, &index); err != nil {
			return errors.Wrap(err, "decode aria2 task index")
		}
	}
	index[record.GID] = record.CreatedAt
	next, err := json.Marshal(index)
	if err != nil {
		return errors.Wrap(err, "marshal aria2 task index")
	}
	if err := s.opts.NamespaceKV.Set(ctx, aria2TaskIndexKey, next); err != nil {
		return errors.Wrap(err, "save aria2 task index")
	}
	return nil
}

func (s *Server) handleUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	cfg := config.Get()
	sessions, sessionsErr := s.listUserSessions(r.Context())
	resp := map[string]any{
		fieldNamespace:  s.namespace(),
		"watch_running": s.watchRunning(),
		"allowed_users": cfg.Bot.AllowedUsers,
		"sessions":      sessions,
	}
	if sessionsErr != nil {
		resp["sessions_error"] = sessionsErr.Error()
	}
	if s.opts.NamespaceKV == nil {
		resp["valid"] = false
		resp["status"] = "namespace kv storage is not configured"
		writeJSON(w, http.StatusOK, resp)
		return
	}

	checkCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	user, err := login.CheckSession(checkCtx, login.SessionOptions{
		KV:               s.opts.NamespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	})
	if err != nil {
		resp["valid"] = false
		resp["status"] = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp["valid"] = true
	resp["status"] = "authorized"
	resp["user"] = telegramUserInfo(user)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUserSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestReboot == nil {
		writeError(w, http.StatusBadRequest, errors.New("reboot is not available in this mode"))
		return
	}
	var req struct {
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	namespace, err := config.NormalizeNamespace(req.Namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	cfg := config.Get()
	if cfg != nil && cfg.Namespace == namespace {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":           true,
			fieldNamespace: namespace,
			fieldMessage:   "当前已经是该用户。",
		})
		return
	}
	exists, err := s.userSessionExists(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !exists {
		writeError(w, http.StatusBadRequest, errors.New("请选择已有登录用户，未登录的用户请先在用户登录页面完成登录。"))
		return
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	next, err := cloneConfig(cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	next.Namespace = namespace
	if err := config.Set(next); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		fieldNamespace: namespace,
		fieldMessage:   "用户已切换，正在重启以加载该用户的数据。",
	})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestReboot()
	}()
}

type userSessionOption struct {
	Namespace string `json:"namespace"`
	Current   bool   `json:"current"`
}

func (s *Server) listUserSessions(ctx context.Context) ([]userSessionOption, error) {
	if s.opts.KVEngine == nil {
		return nil, errors.New("kv engine is not configured")
	}
	namespaces, err := s.opts.KVEngine.Namespaces()
	if err != nil {
		return nil, errors.Wrap(err, "list user session files")
	}
	sort.Strings(namespaces)

	current := s.namespace()
	sessions := make([]userSessionOption, 0, len(namespaces))
	seen := map[string]struct{}{}
	for _, namespace := range namespaces {
		normalized, err := config.NormalizeNamespace(namespace)
		if err != nil || normalized != namespace {
			continue
		}
		if _, ok := seen[namespace]; ok {
			continue
		}
		kvd, err := s.opts.KVEngine.Open(namespace)
		if err != nil {
			continue
		}
		session, err := kvd.Get(ctx, userSessionKey)
		if err != nil || len(session) == 0 {
			continue
		}
		seen[namespace] = struct{}{}
		sessions = append(sessions, userSessionOption{
			Namespace: namespace,
			Current:   namespace == current,
		})
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Current != sessions[j].Current {
			return sessions[i].Current
		}
		return sessions[i].Namespace < sessions[j].Namespace
	})
	return sessions, nil
}

func (s *Server) userSessionExists(ctx context.Context, namespace string) (bool, error) {
	sessions, err := s.listUserSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.Namespace == namespace {
			return true, nil
		}
	}
	return false, nil
}

func (s *Server) handleUserDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	namespace, err := config.NormalizeNamespace(req.Namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if namespace == s.namespace() {
		writeError(w, http.StatusBadRequest, errors.New("当前用户正在运行中，请先切换到其他用户后再删除。"))
		return
	}

	deleted, err := s.deleteUserSession(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		fieldNamespace: namespace,
		fieldDeleted:   deleted,
		fieldMessage:   "用户登录数据已删除。",
	})
}

func (s *Server) deleteUserSession(ctx context.Context, namespace string) (int, error) {
	if s.opts.KVEngine == nil {
		return 0, errors.New("kv engine is not configured")
	}
	exists, err := s.userSessionExists(ctx, namespace)
	if err != nil {
		return 0, err
	}
	if !exists {
		return 0, errors.New("请选择已有登录用户。")
	}

	kvd, err := s.opts.KVEngine.Open(namespace)
	if err != nil {
		return 0, errors.Wrap(err, "open user storage")
	}

	deleted := 0
	for _, key := range []string{userSessionKey, userAppKey} {
		ok, err := deleteUserKey(ctx, kvd, key)
		if err != nil {
			return deleted, err
		}
		if ok {
			deleted++
		}
	}
	return deleted, nil
}

func deleteUserKey(ctx context.Context, kvd storage.Storage, key string) (bool, error) {
	if _, err := kvd.Get(ctx, key); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return false, nil
		}
		return false, errors.Wrapf(err, "check user key %s", key)
	}
	if err := kvd.Delete(ctx, key); err != nil {
		return false, errors.Wrapf(err, "delete user key %s", key)
	}
	return true, nil
}

func telegramUserInfo(user *tg.User) map[string]any {
	if user == nil {
		return map[string]any{}
	}
	name := strings.TrimSpace(strings.TrimSpace(user.FirstName + " " + user.LastName))
	return map[string]any{
		"id":         user.ID,
		"username":   user.Username,
		"name":       name,
		"phone":      user.Phone,
		"bot":        user.Bot,
		"premium":    user.Premium,
		"restricted": user.Restricted,
		"verified":   user.Verified,
	}
}

func (s *Server) handleLoginStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
}

func (s *Server) handleLoginPhoneStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Phone     string `json:"phone"`
		Namespace string `json:"namespace"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.startPhone(r.Context(), req.Phone, req.Namespace); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
}

func (s *Server) handleLoginCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.submitCode(req.Code); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
}

func (s *Server) handleLoginPassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
		return
	}
	if err := s.login.submitPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, s.login.status())
}

func (s *Server) handleLoginCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	s.login.cancel()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"config": publicConfig(config.Get())})
	case http.MethodPatch:
		var req struct {
			Values map[string]json.RawMessage `json:"values"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
			return
		}
		next, err := cloneConfig(config.Get())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		for path, raw := range req.Values {
			if strings.EqualFold(strings.TrimSpace(path), "namespace") {
				writeError(w, http.StatusBadRequest, errors.New("namespace must be changed from user management"))
				return
			}
			if isBlankSensitivePatch(path, raw) {
				continue
			}
			if err := setConfigJSONValue(next, path, raw); err != nil {
				writeError(w, http.StatusBadRequest, errors.Wrapf(err, "set %s", path))
				return
			}
		}
		if err := config.Set(next); err != nil {
			writeError(w, http.StatusInternalServerError, errors.Wrap(err, "save config"))
			return
		}
		if s.opts.AfterConfigSave != nil {
			s.opts.AfterConfigSave(next)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"config":     publicConfig(next),
			fieldMessage: "配置已保存。模块开关会立即生效；监听地址、命名空间、Bot Token 等基础连接参数建议重启后再使用。",
		})
	default:
		methodNotAllowed(w, "GET, PATCH")
	}
}

func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if s.opts.ModuleManager == nil {
		writeError(w, http.StatusBadRequest, errors.New("module manager is not available"))
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"modules": s.opts.ModuleManager.ModuleStates()})
	case http.MethodPost:
		var req struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.Wrap(err, "decode request"))
			return
		}
		state, err := s.opts.ModuleManager.SetModuleEnabled(r.Context(), req.ID, req.Enabled)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":      true,
			"module":  state,
			"modules": s.opts.ModuleManager.ModuleStates(),
		})
	default:
		methodNotAllowed(w, "GET, POST")
	}
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, "GET")
		return
	}
	info, err := updater.CheckLatest(r.Context(), config.EffectiveProxy(config.Get()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "update": info})
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestUpdate == nil {
		writeError(w, http.StatusBadRequest, errors.New("update is not available in this mode"))
		return
	}
	plan, info, err := updater.DownloadLatest(r.Context(), config.EffectiveProxy(config.Get()))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"update":     info,
		fieldMessage: fmt.Sprintf("更新包已下载，准备更新到 %s 并重启。", info.LatestVersion),
	})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestUpdate(plan)
	}()
}

func (s *Server) handleReboot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	if s.opts.RequestReboot == nil {
		writeError(w, http.StatusBadRequest, errors.New("reboot is not available in this mode"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, fieldMessage: "正在重启 tdl"})
	go func() {
		time.Sleep(200 * time.Millisecond)
		s.opts.RequestReboot()
	}()
}

func publicConfig(cfg *config.Config) *config.Config {
	next, err := cloneConfig(cfg)
	if err != nil {
		next = config.DefaultConfig()
	}
	next.Bot.Token = ""
	next.Aria2.Secret = ""
	next.WebUI.Password = ""
	next.ProxyPassword = ""
	return next
}

func cloneConfig(cfg *config.Config) (*config.Config, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var next config.Config
	if err := json.Unmarshal(data, &next); err != nil {
		return nil, err
	}
	return &next, nil
}

func isBlankSensitivePatch(path string, raw json.RawMessage) bool {
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "bot.token", "aria2.secret", "webui.password", "proxy_password":
	default:
		return false
	}
	var value string
	return json.Unmarshal(raw, &value) == nil && value == ""
}

func setConfigJSONValue(cfg *config.Config, path string, raw json.RawMessage) error {
	return setPathJSONValue(reflect.ValueOf(cfg).Elem(), splitConfigPath(path), raw)
}

func splitConfigPath(path string) []string {
	parts := strings.Split(strings.TrimSpace(path), ".")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func setPathJSONValue(value reflect.Value, path []string, raw json.RawMessage) error {
	value = indirectValue(value)
	if len(path) == 0 {
		return errors.New("empty config path")
	}

	switch value.Kind() {
	case reflect.Struct:
		field, ok := fieldByJSONName(value, path[0])
		if !ok {
			return fmt.Errorf("unknown config key %q", path[0])
		}
		if len(path) == 1 {
			return setReflectJSONValue(field, raw)
		}
		return setPathJSONValue(field, path[1:], raw)
	case reflect.Map:
		if len(path) != 1 {
			return fmt.Errorf("config key %q is not an object", path[0])
		}
		key, err := mapKeyValue(value.Type().Key(), path[0])
		if err != nil {
			return err
		}
		item := reflect.New(value.Type().Elem())
		if err := json.Unmarshal(raw, item.Interface()); err != nil {
			return err
		}
		if value.IsNil() {
			value.Set(reflect.MakeMap(value.Type()))
		}
		value.SetMapIndex(key, item.Elem())
		return nil
	default:
		return fmt.Errorf("config key %q cannot be expanded", path[0])
	}
}

func setReflectJSONValue(value reflect.Value, raw json.RawMessage) error {
	if !value.CanSet() {
		return errors.New("config value cannot be set")
	}
	target := reflect.New(value.Type())
	if err := json.Unmarshal(raw, target.Interface()); err != nil {
		return err
	}
	value.Set(target.Elem())
	return nil
}

func fieldByJSONName(value reflect.Value, name string) (reflect.Value, bool) {
	value = indirectValue(value)
	typ := value.Type()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}
		if strings.EqualFold(jsonName, name) || strings.EqualFold(field.Name, name) {
			return value.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func indirectValue(value reflect.Value) reflect.Value {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func mapKeyValue(typ reflect.Type, raw string) (reflect.Value, error) {
	switch typ.Kind() {
	case reflect.String:
		return reflect.ValueOf(raw).Convert(typ), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported map key type %s", typ)
	}
}

func (s *Server) handleAria2Proxy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, "POST")
		return
	}
	cfg := config.Get()
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, errors.Wrap(err, "read request"))
		return
	}
	if cfg.Aria2.Secret != "" || bytes.Contains(body, []byte("/download/")) {
		connections := config.HTTPRangeConnectionsFor(cfg.HTTP, config.EffectiveThreads(cfg))
		body, err = rewriteAria2ProxyRequest(body, cfg.HTTP.PublicBaseURL, cfg.Aria2.Secret, connections)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	timeout := time.Duration(cfg.Aria2.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.Aria2.RPCURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, errors.Wrap(err, "create aria2 request"))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, errors.Wrap(err, "forward aria2 request"))
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func injectAria2Secret(body []byte, secret string) ([]byte, error) {
	return rewriteAria2ProxyRequest(body, "", secret, 1)
}

func rewriteAria2ProxyRequest(body []byte, publicBaseURL, secret string, connections int) ([]byte, error) {
	var payload any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return nil, errors.Wrap(err, "decode aria2 request")
	}
	token := ""
	if secret != "" {
		token = "token:" + secret
	}
	switch value := payload.(type) {
	case map[string]any:
		rewriteAria2RequestObject(value, publicBaseURL, token, connections)
	case []any:
		for _, item := range value {
			if obj, ok := item.(map[string]any); ok {
				rewriteAria2RequestObject(obj, publicBaseURL, token, connections)
			}
		}
	default:
		return nil, errors.New("aria2 request must be an object or array")
	}
	next, err := json.Marshal(payload)
	if err != nil {
		return nil, errors.Wrap(err, "encode aria2 request")
	}
	return next, nil
}

func rewriteAria2RequestObject(request map[string]any, publicBaseURL, token string, connections int) {
	normalizeAria2AddURIRequest(request, publicBaseURL, connections)
	if token != "" {
		addAria2Token(request, token)
	}
}

func addAria2Token(request map[string]any, token string) {
	method, _ := request["method"].(string)
	if method == "system.multicall" {
		addAria2MulticallToken(request, token)
		return
	}
	if strings.HasPrefix(method, "system.") {
		return
	}
	prependAria2TokenParam(request, token)
}

func addAria2MulticallToken(request map[string]any, token string) {
	params, _ := request["params"].([]any)
	if len(params) == 0 {
		return
	}
	if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
		params = params[1:]
	}
	if len(params) == 0 {
		request["params"] = []any{}
		return
	}
	calls, ok := params[0].([]any)
	if !ok {
		request["params"] = params
		return
	}
	for _, call := range calls {
		obj, ok := call.(map[string]any)
		if !ok {
			continue
		}
		methodName, _ := obj["methodName"].(string)
		if strings.HasPrefix(methodName, "system.") {
			continue
		}
		prependAria2TokenParam(obj, token)
	}
	request["params"] = append([]any{calls}, params[1:]...)
}

func prependAria2TokenParam(request map[string]any, token string) {
	params, _ := request["params"].([]any)
	if len(params) > 0 {
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			return
		}
	}
	request["params"] = append([]any{token}, params...)
}

func normalizeAria2AddURIRequest(request map[string]any, publicBaseURL string, connections int) {
	method, _ := request["method"].(string)
	if method == "system.multicall" {
		params, _ := request["params"].([]any)
		if len(params) == 0 {
			return
		}
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			params = params[1:]
		}
		if len(params) == 0 {
			return
		}
		calls, ok := params[0].([]any)
		if !ok {
			return
		}
		for _, call := range calls {
			if obj, ok := call.(map[string]any); ok {
				normalizeAria2MulticallAddURIRequest(obj, publicBaseURL, connections)
			}
		}
		return
	}
	if method != "aria2.addUri" {
		return
	}
	normalizeAria2AddURIParams(request, publicBaseURL, connections)
}

func normalizeAria2MulticallAddURIRequest(request map[string]any, publicBaseURL string, connections int) {
	method, _ := request["methodName"].(string)
	if method != "aria2.addUri" {
		return
	}
	normalizeAria2AddURIParams(request, publicBaseURL, connections)
}

func normalizeAria2AddURIParams(request map[string]any, publicBaseURL string, connections int) {
	params, _ := request["params"].([]any)
	paramStart := 0
	if len(params) > 0 {
		if first, ok := params[0].(string); ok && strings.HasPrefix(first, "token:") {
			paramStart = 1
		}
	}
	if len(params) <= paramStart {
		return
	}
	urls, ok := params[paramStart].([]any)
	if !ok || !hasTDLDownloadURI(urls, publicBaseURL) {
		return
	}

	optionIndex := paramStart + 1
	var options map[string]any
	if len(params) > optionIndex {
		options, _ = params[optionIndex].(map[string]any)
	}
	if options == nil {
		options = map[string]any{}
		if len(params) > optionIndex {
			params[optionIndex] = options
		} else {
			params = append(params, options)
		}
	}
	applyTDLAria2HTTPConnectionOptions(options, connections)
	request["params"] = params
}

func hasTDLDownloadURI(urls []any, publicBaseURL string) bool {
	for _, raw := range urls {
		value, ok := raw.(string)
		if ok && isTDLDownloadURI(value, publicBaseURL) {
			return true
		}
	}
	return false
}

func isTDLDownloadURI(raw, publicBaseURL string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}

	expectedPath := "/download/"
	var base *url.URL
	if publicBaseURL != "" {
		if parsed, err := url.Parse(publicBaseURL); err == nil {
			base = parsed
			expectedPath = strings.TrimRight(parsed.Path, "/") + "/download/"
		}
	}

	if base != nil && base.Host != "" && u.Host != "" {
		if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
			return false
		}
	}
	return strings.HasPrefix(u.Path, expectedPath)
}

func fetchAria2Statuses(ctx context.Context, cfg config.Aria2Config) (map[string]aria2Status, error) {
	out := map[string]aria2Status{}
	if cfg.RPCURL == "" {
		return out, nil
	}
	for _, call := range []struct {
		method string
		params []any
	}{
		{method: "aria2.tellActive", params: []any{aria2StatusKeys()}},
		{method: "aria2.tellWaiting", params: []any{0, 1000, aria2StatusKeys()}},
		{method: "aria2.tellStopped", params: []any{0, 1000, aria2StatusKeys()}},
	} {
		var statuses []aria2Status
		if err := callAria2(ctx, cfg, call.method, call.params, &statuses); err != nil {
			return out, err
		}
		for _, status := range statuses {
			if status.GID != "" {
				out[status.GID] = status
			}
		}
	}
	return out, nil
}

func aria2StatusKeys() []string {
	return []string{"gid", "status", "totalLength", "completedLength", "errorCode", "errorMessage", "files"}
}

func callAria2(ctx context.Context, cfg config.Aria2Config, method string, params []any, result any) error {
	if cfg.Secret != "" {
		params = append([]any{"token:" + cfg.Secret}, params...)
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "tdl-webui",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.RPCURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	if decoded.Error != nil {
		return fmt.Errorf("aria2 rpc error %d: %s", decoded.Error.Code, decoded.Error.Message)
	}
	return json.Unmarshal(decoded.Result, result)
}

func aria2Lengths(status aria2Status) (total, completed int64) {
	total = parseAria2Length(status.TotalLength)
	completed = parseAria2Length(status.CompletedLength)
	if total == 0 && len(status.Files) > 0 {
		for _, file := range status.Files {
			total += parseAria2Length(file.Length)
			completed += parseAria2Length(file.CompletedLength)
		}
	}
	return total, completed
}

func parseAria2Length(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func normalizedAria2Status(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func downloadURL(baseURL, taskID string) string {
	if baseURL == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "/download/" + url.PathEscape(taskID)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/download/" + url.PathEscape(taskID)
	return u.String()
}

func isDownloadTaskRecordKey(key string) bool {
	return strings.HasPrefix(key, downloadTaskKeyPrefix) && key != downloadTaskIndexKey
}

func (s *Server) namespace() string {
	if s.opts.Namespace != "" {
		return s.opts.Namespace
	}
	cfg := config.Get()
	if cfg != nil {
		return cfg.Namespace
	}
	return fieldDefault
}

func (s *Server) internalDownloadController() *watch.InternalDownloadController {
	return watch.NewInternalDownloadController(s.opts.NamespaceKV)
}

func (s *Server) watchRunning() bool {
	if s.opts.WatchRunning == nil {
		return false
	}
	return s.opts.WatchRunning()
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]any{
		"ok":    false,
		"error": err.Error(),
	})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

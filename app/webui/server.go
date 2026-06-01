package webui

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/tg"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

//go:embed index.html login.html aria2ng.html static views
var assets embed.FS

func init() {
	// Serve module scripts and stylesheets with a strict, correct MIME type
	// regardless of host OS registry settings; browsers refuse to evaluate ES
	// modules delivered with a non-JavaScript content type.
	_ = mime.AddExtensionType(".js", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8")
	_ = mime.AddExtensionType(".css", "text/css; charset=utf-8")
}

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

	actionDelete = "delete"
	actionPause  = "pause"
	actionResume = "resume"
	fieldItems   = "items"
	fieldRunning = "running"
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
	mux.HandleFunc("/api/forwards", s.authFunc(s.handleForwards))
	mux.HandleFunc("/api/forwards/actions", s.authFunc(s.handleForwardActions))
	mux.HandleFunc("/api/kv/links", s.authFunc(s.handleKVLinks))
	mux.HandleFunc("/api/kv/links/actions", s.authFunc(s.handleKVActions))
	mux.HandleFunc("/api/kv/links/", s.authFunc(s.handleKVLink))
	mux.HandleFunc("/api/user", s.authFunc(s.handleUser))
	mux.HandleFunc("/api/user/switch", s.authFunc(s.handleUserSwitch))
	mux.HandleFunc("/api/user/delete", s.authFunc(s.handleUserDelete))
	mux.HandleFunc("/api/user/spam-check", s.authFunc(s.handleSpamCheck))
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
	mux.HandleFunc("/", s.authFunc(s.handleAppShell))

	return mux
}

// handleAppShell serves the single-page app shell for any HTML route that is
// not an API, view fragment, or aria2 endpoint. The client-side router then
// resolves the path (e.g. /dashboard, /config) to a view, so refreshing or deep
// linking to a path lands on the correct page instead of falling back to "/".
func (s *Server) handleAppShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/views/") || strings.HasPrefix(r.URL.Path, "/aria2/") {
		http.NotFound(w, r)
		return
	}
	s.serveAsset(w, r, "index.html", "text/html; charset=utf-8")
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

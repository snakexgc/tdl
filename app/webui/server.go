package webui

import (
	"context"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gotd/td/tg"

	appforward "github.com/snakexgc/tdl/app/forward"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/app/reset"
	"github.com/snakexgc/tdl/application"
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	downloadcontrol "github.com/snakexgc/tdl/application/download.control"
	local "github.com/snakexgc/tdl/application/downloader.local"
	panel "github.com/snakexgc/tdl/application/panel.webui"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/bsw/services/telemetry"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

var assets = application.WebAssets()

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
	aria2TaskKeyPrefix    = taskhub.Aria2Prefix
	aria2TaskIndexKey     = taskhub.Aria2Index

	aria2StatusComplete    = "complete"
	tdlAria2PieceSize      = "1024K"
	tdlAria2TimeoutSeconds = "600"

	userSessionKey = "session"
	userAppKey     = "app"

	webUICookieName         = "tdl_webui_session"
	webUISessionTTL         = 24 * time.Hour
	webUILoginFailureWindow = time.Minute
	webUILoginLockout       = 5 * time.Minute
	webUIMaxLoginFailures   = 5

	fieldUsingDefaultCredentials = "using_default_credentials"
	fieldNamespace               = "namespace"
	fieldDeleted                 = "deleted"
	valueTrue                    = "true"
	fieldMessage                 = "message"
	fieldError                   = "error"
	fieldDefault                 = "default"

	actionDelete = "delete"
	actionPause  = "pause"
	actionResume = "resume"
	fieldItems   = "items"
	fieldRunning = "running"
)

type Options struct {
	Ready                func()
	Dialogs              ports.DialogCatalog
	Catalog              *rte.Catalog
	ComponentStore       *rteconfig.Store
	SessionChecker       ports.AccountSession
	Connections          *tgauth.Connections
	SetComponentHost     func(*rte.Runtime)
	DownloadControl      ports.DownloadControl
	LocalLinks           ports.DownloadExecutor
	Credentials          ports.TelegramCredentials
	Updater              ports.Updater
	ComponentManager     ComponentManager
	ForwardQueue         ports.ForwardTasks
	Context              context.Context
	KVEngine             kv.Storage
	Namespace            string
	NamespaceKV          storage.Storage
	ConfigurationManager ports.ConfigurationManager
	OnLoginSuccess       func(*tg.User)
	RequestReboot        func()
	ResetPlan            *reset.Plan
	RequestReset         func()
	RequestUpdate        func(types.UpdatePlan)
	WatchRunning         func() bool
}

type ComponentManager interface {
	ComponentConfigurations() ([]rte.Configuration, bool)
	SaveComponentConfiguration(context.Context, string, map[string]any) error
}

type ComponentDiagnostics interface {
	ComponentHealth() []rte.Health
}

type Server struct {
	dialogs        ports.DialogCatalog
	samples        *telemetry.Sampler
	assets         fs.FS
	accountActions *accounttelegram.Actions
	opts           Options

	login               *webLoginManager
	configuration       ports.Configuration
	activeConfiguration ports.SystemConfiguration
	sessionCatalog      ports.SessionCatalog
	downloadLinksPort   ports.DownloadLinks
	downloadCatalogPort ports.DownloadCatalog

	sessionMu sync.Mutex
	sessions  map[string]time.Time
	loginMu   sync.Mutex
	logins    map[string]loginFailure

	dashboardMu         sync.Mutex
	dashboardLastBytes  int64
	dashboardLastSample time.Time

	shutdownRequested atomic.Bool
}

func Run(ctx context.Context, opts Options) error {
	cfg := config.From(ctx)
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
	defer func() { _ = server.accountActions.Stop(context.Background()) }()
	defer func() { _ = server.login.Stop(context.Background()) }()
	completed := make(chan error, 1)
	host, err := application.PanelHostStored(ctx, types.AccountID(opts.Namespace), panel.Options{
		Configuration:   server.configuration,
		Sessions:        server.sessionCatalog,
		DownloadLinks:   server.downloadLinksPort,
		DownloadCatalog: server.downloadCatalogPort,
		Login:           server.login.AccountLogin,
		Address:         config.WebUIListenAddr(cfg), Handler: server.routes(), Completed: completed,
	}, opts.ComponentStore, server.accountActions)
	if err != nil {
		return err
	}
	if opts.SetComponentHost != nil {
		opts.SetComponentHost(host)
		defer opts.SetComponentHost(nil)
	}
	defer func() { _ = host.Stop(context.Background()) }()
	if opts.Ready != nil {
		opts.Ready()
	}
	select {
	case <-ctx.Done():
		return nil
	case err := <-completed:
		return err
	}
}

func NewServer(opts Options) *Server {
	if opts.Catalog == nil {
		var err error
		opts.Catalog, err = application.Catalog()
		if err != nil {
			panic(err)
		}
	}
	if opts.ForwardQueue == nil {
		opts.ForwardQueue = appforward.NewQueue(opts.NamespaceKV)
	}
	var active ports.SystemConfiguration
	if cfg := config.From(opts.Context); cfg != nil {
		active = config.System(cfg)
	}
	server := &Server{
		samples:             telemetry.New(time.Second),
		assets:              application.WebAssets(opts.Catalog),
		configuration:       panel.NewConfiguration(configurationStore{manager: opts.ConfigurationManager, active: active}),
		activeConfiguration: active,
		opts:                opts,
		login:               newWebLoginManager(opts),
		sessions:            map[string]time.Time{},
		logins:              map[string]loginFailure{},
	}
	server.dialogs = opts.Dialogs
	if server.dialogs == nil {
		server.dialogs = accounttelegram.NewDialogs(login.DialogTransport{Options: func() login.SessionOptions { return server.login.sessionOptions(server.namespace(), opts.NamespaceKV) }})
	}
	server.sessionCatalog = accounttelegram.NewSessions(server.namespace(), tgauth.SessionRepository{Engine: opts.KVEngine, Connections: opts.Connections})
	server.downloadLinksPort = downloadcontrol.NewLinkControl(types.AccountID(server.namespace()), taskhub.LinkRepository{Store: opts.NamespaceKV, Engine: opts.KVEngine, Namespace: server.namespace()}, server.localDownloadController())
	server.downloadCatalogPort = downloadcontrol.NewCatalog(types.AccountID(server.namespace()), catalogAdapter{server: server, repository: taskhub.LinkRepository{Store: opts.NamespaceKV, Engine: opts.KVEngine, Namespace: server.namespace()}})
	server.accountActions = accounttelegram.NewActions(opts.Context, types.AccountID(server.namespace()), server.sessionCatalog, accountSelection{}, login.SpamProbe{Options: func() login.SessionOptions { return server.login.sessionOptions(server.namespace(), opts.NamespaceKV) }})
	return server
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()

	staticFS, _ := fs.Sub(s.assets, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	handlers := map[string]http.HandlerFunc{
		"/login":                      s.handleLoginPage,
		"/api/auth/session":           s.handleAuthSession,
		"/api/auth/login":             s.handleAuthLogin,
		"/api/auth/logout":            s.handleAuthLogout,
		"/views/":                     s.handleViewAsset,
		"/aria2ng.html":               s.handleAsset("aria2ng.html", "text/html; charset=utf-8"),
		"/aria2/jsonrpc":              s.handleAria2Proxy,
		"/api/heartbeat":              s.handleHeartbeat,
		"/api/events":                 s.handleEvents,
		"/api/dashboard":              s.handleDashboard,
		"/api/status":                 s.handleStatus,
		"/api/aria2/check":            s.handleAria2Check,
		"/api/download-tasks":         s.handleDownloadTasks,
		"/api/download-tasks/actions": s.handleDownloadTaskActions,
		"/api/forwards":               s.handleForwards,
		"/api/forwards/actions":       s.handleForwardActions,
		"/api/kv/links":               s.handleKVLinks,
		"/api/kv/links/actions":       s.handleKVActions,
		"/api/kv/links/":              s.handleKVLink,
		"/api/user":                   s.handleUser,
		"/api/dialogs":                s.handleDialogs,
		"/api/user/switch":            s.handleUserSwitch,
		"/api/user/delete":            s.handleUserDelete,
		"/api/user/spam-check":        s.handleSpamCheck,
		"/api/login/status":           s.handleLoginStatus,
		"/api/login/phone/start":      s.handleLoginPhoneStart,
		"/api/login/code":             s.handleLoginCode,
		"/api/login/password":         s.handleLoginPassword,
		"/api/login/cancel":           s.handleLoginCancel,
		"/api/components":             s.handleComponents,
		"/api/components/health":      s.handleComponentHealth,
		"/api/logs":                   s.handleLogs,
		"/api/config":                 s.handleConfig,
		"/api/update/check":           s.handleUpdateCheck,
		"/api/update/apply":           s.handleUpdateApply,
		"/api/system/reboot":          s.handleReboot,
		"/api/system/reset":           s.handleReset,
		"/":                           s.handleAppShell,
	}
	for _, route := range application.WebRoutes(s.opts.Catalog) {
		handler := handlers[route.Path]
		if route.Port != "" {
			if handler != nil {
				panic("duplicate component route adapter: " + route.Path)
			}
			handler = s.componentAction(route)
		}
		if handler == nil {
			panic("missing component route adapter: " + route.Path)
		}
		if !route.Public {
			handler = s.authFunc(handler)
		}
		mux.HandleFunc(route.Path, handler)
		delete(handlers, route.Path)
	}
	if len(handlers) != 0 {
		panic("unowned web route adapters")
	}

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
	cfg := config.From(s.opts.Context)
	if cfg != nil {
		return cfg.Namespace
	}
	return fieldDefault
}

func (s *Server) localDownloadController() *local.Controller {
	return local.NewController(taskhub.NewLocalRepository(s.opts.NamespaceKV))
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
		"ok":       false,
		fieldError: err.Error(),
	})
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

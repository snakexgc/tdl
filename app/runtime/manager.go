package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/app/bot"
	appforward "github.com/snakexgc/tdl/app/forward"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/app/updater"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/app/webui"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

const (
	moduleStopTimeout      = 10 * time.Second
	moduleStatusNotStarted = "未启动"
	moduleStatusRunning    = "运行中"
	moduleIDBot            = "bot"
	moduleIDWatch          = "watch"
	moduleIDHTTP           = "http"
	moduleIDAria2          = "aria2"
	moduleIDForward        = "forward"
)

type Options struct {
	ComponentConfigDir string
	RequestReboot      func()
	RequestUpdate      func(updater.Plan)
}

type aria2ManagerConfig struct {
	RPCURL         string
	Secret         string
	Dir            string
	TimeoutSeconds int
	PublicBaseURL  string
	LinkTTLHours   int
	Limit          int
	PoolSize       int
}

func effectiveAria2ManagerConfig(cfg *config.Config) aria2ManagerConfig {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return aria2ManagerConfig{
		RPCURL:         cfg.Aria2.RPCURL,
		Secret:         cfg.Aria2.Secret,
		Dir:            cfg.Aria2.Dir,
		TimeoutSeconds: cfg.Aria2.TimeoutSeconds,
		PublicBaseURL:  cfg.HTTP.PublicBaseURL,
		LinkTTLHours:   cfg.HTTP.DownloadLinkTTLHours,
		Limit:          config.EffectiveLimit(cfg),
		PoolSize:       config.EffectivePoolSize(cfg),
	}
}

func watchAutoDownloadEnabled(cfg *config.Config) bool {
	return cfg != nil &&
		cfg.Modules.Watch &&
		cfg.Modules.Aria2 &&
		cfg.Aria2.AutoDownload &&
		config.EffectiveDownloaderMode(cfg) == config.DownloaderModeAria2
}

type Manager struct {
	accountHost     *rte.Runtime
	sessionPort     ports.AccountSession
	connections     *tgauth.Connections
	intentHost      *rte.Runtime
	botProcess      *rte.Process
	aria2Process    *rte.Process
	panelProcess    *rte.Process
	panelHost       *rte.Runtime
	localHost       *rte.Runtime
	botComponents   *rte.Runtime
	componentStore  *rteconfig.Store
	policyErr       error
	policies        *rte.Runtime
	filter          ports.FilterRules
	naming          ports.NamingRules
	parent          context.Context
	forwardQueue    *appforward.Queue
	downloadAccount types.AccountID
	downloadHost    *rte.Runtime
	downloadPort    ports.DownloadControl

	kvEngine    kv.Storage
	namespaceKV storage.Storage
	watchCtrl   *watch.Controller
	httpService *httpdl.Service
	httpCtrl    *httpdl.Controller
	aria2Mgr    *aria2.Manager
	aria2Err    error
	aria2Config aria2ManagerConfig

	requestReboot func()
	requestUpdate func(updater.Plan)

	applyMu        sync.Mutex
	transitionMu   sync.Mutex
	applyVersion   atomic.Uint64
	mu             sync.Mutex
	notify         watch.NotifyFunc
	botStatus      string
	botErr         error
	watchMode      string
	watchEnabled   bool
	forwardEnabled bool
	aria2Enabled   bool
	aria2Auto      bool
}

func Run(ctx context.Context, opts Options) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	engine := kv.From(runCtx)
	namespaceKV, err := engine.Open(config.Get().Namespace)
	if err != nil {
		return errors.Wrap(err, "open kv storage")
	}

	opts.RequestReboot = wrapShutdown(cancel, opts.RequestReboot)
	opts.RequestUpdate = wrapUpdateShutdown(cancel, opts.RequestUpdate)

	manager := NewManager(runCtx, engine, namespaceKV, opts)
	webStarted := manager.StartWebUI(runCtx)
	manager.ApplyConfig(config.Get())

	if !webStarted && !manager.hasRunnableModule(config.Get()) {
		manager.Shutdown()
		return errors.New("please configure webui.address, webui.port, webui.username and webui.password, or configure bot.token")
	}

	<-runCtx.Done()
	manager.Shutdown()
	return nil
}

func wrapShutdown(cancel context.CancelFunc, fn func()) func() {
	return func() {
		if fn != nil {
			fn()
		}
		cancel()
	}
}

func wrapUpdateShutdown(cancel context.CancelFunc, fn func(updater.Plan)) func(updater.Plan) {
	return func(plan updater.Plan) {
		if fn != nil {
			fn(plan)
		}
		cancel()
	}
}

func NewManager(ctx context.Context, engine kv.Storage, namespaceKV storage.Storage, opts Options) *Manager {
	cfg := config.Get()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var componentStore *rteconfig.Store
	if opts.ComponentConfigDir != "" {
		componentStore = rteconfig.NewStore(opts.ComponentConfigDir)
	}
	host, filter, naming, policyErr := newPolicyHostStored(ctx, cfg, componentStore)
	manager := &Manager{
		connections:     tgauth.NewConnections(ctx),
		downloadAccount: types.AccountID(cfg.Namespace),
		componentStore:  componentStore,
		policies:        host, filter: filter, naming: naming, policyErr: policyErr,
		parent:         ctx,
		forwardQueue:   appforward.NewQueue(namespaceKV),
		kvEngine:       engine,
		namespaceKV:    namespaceKV,
		requestReboot:  opts.RequestReboot,
		requestUpdate:  opts.RequestUpdate,
		botStatus:      moduleStatusNotStarted,
		watchMode:      config.EffectiveDownloaderMode(cfg),
		watchEnabled:   cfg.Modules.Watch,
		forwardEnabled: cfg.Modules.Forward,
		aria2Enabled:   cfg.Modules.Aria2,
		aria2Auto:      watchAutoDownloadEnabled(cfg),
		aria2Config:    effectiveAria2ManagerConfig(cfg),
	}
	if manager.downloadAccount == "" {
		manager.downloadAccount = types.DefaultAccount
	}
	accountHost, resourceErr := application.AccountResourceHost(ctx, manager.downloadAccount, manager.connections, login.SessionProbe{Options: manager.sessionOptions})
	manager.accountHost = accountHost
	if resourceErr == nil {
		var value any
		value, resourceErr = accountHost.Resolve(ports.AccountSessionName)
		if resourceErr == nil {
			manager.sessionPort = value.(ports.AccountSession)
		}
	}
	manager.policyErr = errors.Join(manager.policyErr, resourceErr)
	manager.botProcess = rte.NewProcess(ctx, manager.downloadAccount, "host.bot")
	manager.aria2Process = rte.NewProcess(ctx, manager.downloadAccount, "host.aria2")
	manager.panelProcess = rte.NewProcess(ctx, manager.downloadAccount, "host.panel")
	manager.forwardQueue.SetNotifier(manager.Notify)
	manager.initDownloadControl(ctx)
	manager.httpService = httpdl.NewService(cfg, namespaceKV, logctx.From(ctx))
	manager.httpService.Proxy().SetComponentStore(manager.componentStore)
	manager.httpCtrl = httpdl.NewController(ctx, manager.httpService)
	manager.aria2Mgr = aria2.NewManager(cfg, namespaceKV, logctx.From(ctx))
	manager.aria2Mgr.SetComponentStore(manager.componentStore)
	manager.watchCtrl = watch.NewController(ctx, manager.watchOptions(cfg), manager.Notify)
	return manager
}

func (m *Manager) StartWebUI(ctx context.Context) bool {
	cfg := config.Get()
	if cfg == nil || strings.TrimSpace(config.WebUIListenAddr(cfg)) == "" {
		color.Yellow("Web 管理面板未启动：webui.address 或 webui.port 为空。")
		return false
	}
	if strings.TrimSpace(cfg.WebUI.Username) == "" || cfg.WebUI.Password == "" {
		color.Yellow("Web 管理面板未启动：请设置 webui.username 和 webui.password。")
		return false
	}

	errCh := make(chan error, 1)
	_, startErr := m.panelProcess.Start(func(ctx context.Context) error {
		err := webui.Run(ctx, webui.Options{
			SetComponentHost: func(host *rte.Runtime) { m.mu.Lock(); m.panelHost = host; m.mu.Unlock() },
			Connections:      m.connections,
			SessionChecker:   m.sessionPort,
			Credentials:      m, Updater: m,
			DownloadControl:  m,
			KVEngine:         m.kvEngine,
			ForwardQueue:     m.forwardQueue,
			Namespace:        cfg.Namespace,
			NamespaceKV:      m.namespaceKV,
			AfterConfigSave:  m.ApplyConfig,
			OnLoginSuccess:   m.onLoginSuccess,
			RequestReboot:    m.requestReboot,
			RequestUpdate:    m.requestUpdate,
			WatchRunning:     m.watchCtrl.Running,
			ModuleManager:    m,
			ComponentManager: m,
		})
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			color.Yellow("WebUI stopped: %v", err)
		}
		errCh <- err
		return err
	}, rte.Recovery{})
	if startErr != nil {
		return false
	}

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			return false
		}
	case <-time.After(200 * time.Millisecond):
	}
	color.Green("WebUI: http://%s", config.WebUIListenAddr(cfg))
	return true
}

func (m *Manager) ApplyConfig(cfg *config.Config) {
	if m == nil {
		return
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	if cfg == nil {
		cfg = config.Get()
	}
	if cfg == nil {
		return
	}
	version := m.applyVersion.Add(1)
	if err := m.applyConfigLocked(cfg, version, true, context.Background()); err != nil {
		logctx.From(m.parent).Error("apply configuration failed", zap.Error(err))
	}
}

// applyConfigLocked reconciles every managed module against one config
// generation. The caller must hold applyMu. Background stops are generation
// checked and serialized with starts, so an older ApplyConfig cannot stop a
// module that a newer config has already enabled.
func (m *Manager) applyConfigLocked(cfg *config.Config, version uint64, async bool, watchCtx context.Context) error {
	if watchCtx == nil {
		watchCtx = context.Background()
	}
	m.mu.Lock()
	host := m.policies
	m.mu.Unlock()
	if host == nil {
		next, filter, naming, err := newPolicyHostStored(m.parent, cfg, m.componentStore)
		m.mu.Lock()
		m.policies, m.filter, m.naming, m.policyErr = next, filter, naming, err
		m.mu.Unlock()
	} else if m.componentStore == nil {
		if err := host.ReconfigureBatch(watchCtx, daemonComponentValues(cfg)); err != nil {
			return fmt.Errorf("reconfigure policy host: %w", err)
		}
	}
	restartHTTP := m.httpService.UpdateConfig(cfg)
	nextWatchMode := config.EffectiveDownloaderMode(cfg)
	nextAria2Auto := watchAutoDownloadEnabled(cfg)
	nextAria2Config := effectiveAria2ManagerConfig(cfg)
	m.mu.Lock()
	prevWatchMode := m.watchMode
	prevWatchEnabled := m.watchEnabled
	prevForwardEnabled := m.forwardEnabled
	prevAria2Auto := m.aria2Auto
	aria2ConfigChanged := m.aria2Config != nextAria2Config
	m.watchMode = nextWatchMode
	m.watchEnabled = cfg.Modules.Watch
	m.forwardEnabled = cfg.Modules.Forward
	m.aria2Enabled = cfg.Modules.Aria2
	m.aria2Auto = nextAria2Auto
	m.mu.Unlock()

	if aria2ConfigChanged {
		var stopErr error
		m.transition(version, func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), moduleStopTimeout)
			defer cancel()
			if stopErr = m.stopAria2(stopCtx); stopErr != nil {
				return
			}
			manager := aria2.NewManager(cfg, m.namespaceKV, logctx.From(m.parent))
			manager.SetComponentStore(m.componentStore)
			m.mu.Lock()
			m.aria2Mgr = manager
			m.aria2Config = nextAria2Config
			m.mu.Unlock()
		})
		if stopErr != nil {
			return stopErr
		}
	}
	m.watchCtrl.UpdateOptions(m.watchOptions(cfg))
	restartWatch := m.watchCtrl.Running() &&
		((prevWatchMode != "" && prevWatchMode != nextWatchMode) ||
			prevWatchEnabled != cfg.Modules.Watch ||
			prevForwardEnabled != cfg.Modules.Forward ||
			prevAria2Auto != nextAria2Auto ||
			(aria2ConfigChanged && nextAria2Auto))

	if cfg.Modules.HTTP {
		var stopErr error
		m.transition(version, func() {
			if restartHTTP && m.httpCtrl.Running() {
				stopCtx, cancel := context.WithTimeout(watchCtx, moduleStopTimeout)
				defer cancel()
				if stopErr = m.httpCtrl.StopContext(stopCtx); stopErr != nil {
					return
				}
			}
			m.StartHTTP()
		})
		if stopErr != nil {
			return stopErr
		}
	} else {
		m.stopForConfig(version, async, m.StopHTTP)
	}
	if cfg.Modules.Aria2 {
		m.transition(version, m.StartAria2Manager)
	} else {
		m.stopForConfig(version, async, m.StopAria2Manager)
	}

	if cfg.Modules.Bot {
		m.transition(version, m.StartBot)
	} else {
		m.stopForConfig(version, async, m.StopBot)
	}
	if cfg.Modules.Watch || cfg.Modules.Forward {
		if restartWatch {
			if async {
				m.runTransition(version, func() {
					if err := m.restartWatch(m.parent); err != nil {
						logctx.From(m.parent).Error("restart watch", zap.Error(err))
					}
				})
			} else {
				var err error
				m.transition(version, func() {
					err = m.restartWatch(watchCtx)
				})
				if err != nil {
					return err
				}
			}
		} else if async {
			m.runTransition(version, func() { _ = m.StartWatch(watchCtx) })
		} else {
			var err error
			m.transition(version, func() { err = m.StartWatch(watchCtx) })
			if err != nil {
				return err
			}
		}
	} else {
		m.stopForConfig(version, async, m.StopWatch)
	}
	return nil
}

func (m *Manager) transition(version uint64, fn func()) bool {
	if fn == nil {
		return false
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.applyVersion.Load() != version {
		return false
	}
	fn()
	return true
}

func (m *Manager) runTransition(version uint64, fn func()) {
	go m.transition(version, fn)
}

func (m *Manager) stopForConfig(version uint64, async bool, stop func()) {
	if async {
		m.runTransition(version, stop)
		return
	}
	m.transition(version, stop)
}

func (m *Manager) ModuleStates() []webui.ModuleState {
	cfg := config.Get()
	return []webui.ModuleState{
		{
			ID:          "webui",
			Name:        "Web 管理面板",
			Description: "用于查看状态、修改配置和管理其他模块。该模块正在提供当前页面，不能在这里关闭。",
			Enabled:     true,
			Running:     true,
			CanToggle:   false,
			Status:      moduleStatusRunning,
		},
		m.botState(cfg),
		m.watchState(cfg),
		m.httpState(cfg),
		m.aria2State(cfg),
		m.forwardState(cfg),
	}
}

func (m *Manager) SetModuleEnabled(ctx context.Context, id string, enabled bool) (webui.ModuleState, error) {
	if m == nil {
		return webui.ModuleState{}, errors.New("module manager is not initialized")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()

	next, err := config.Clone(config.Get())
	if err != nil {
		return webui.ModuleState{}, err
	}

	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case moduleIDBot:
		next.Modules.Bot = enabled
	case moduleIDWatch:
		next.Modules.Watch = enabled
	case moduleIDHTTP:
		next.Modules.HTTP = enabled
	case moduleIDAria2:
		next.Modules.Aria2 = enabled
	case moduleIDForward:
		next.Modules.Forward = enabled
	case "webui":
		return webui.ModuleState{}, errors.New("webui cannot be disabled from the web panel")
	default:
		return webui.ModuleState{}, fmt.Errorf("unknown module %q", id)
	}

	if err := config.Set(next); err != nil {
		return webui.ModuleState{}, err
	}
	version := m.applyVersion.Add(1)
	if err := m.applyConfigLocked(next, version, false, ctx); err != nil {
		return webui.ModuleState{}, err
	}

	switch id {
	case moduleIDBot:
		return m.botState(next), nil
	case moduleIDWatch:
		return m.watchState(next), nil
	case moduleIDHTTP:
		return m.httpState(next), nil
	case moduleIDAria2:
		return m.aria2State(next), nil
	case moduleIDForward:
		return m.forwardState(next), nil
	default:
		return webui.ModuleState{}, fmt.Errorf("unknown module %q", id)
	}
}

func (m *Manager) StartBot() {
	cfg := config.Get()
	if cfg == nil || !cfg.Modules.Bot {
		return
	}
	if strings.TrimSpace(cfg.Bot.Token) == "" {
		m.setBotStopped("未启动：请先填写 Telegram Bot Token。", nil)
		return
	}

	started, startErr := m.botProcess.Start(func(ctx context.Context) error {
		err := bot.Run(ctx, bot.Options{
			DownloadControl: m,
			Connections:     m.connections,
			SessionChecker:  m.sessionPort,
			Credentials:     m, Updater: m,
			ComponentStore:        m.componentStore,
			SetComponentHost:      func(host *rte.Runtime) { m.mu.Lock(); m.botComponents = host; m.mu.Unlock() },
			Token:                 cfg.Bot.Token,
			ForwardQueue:          m.forwardQueue,
			AllowedUsers:          cfg.Bot.AllowedUsers,
			Proxy:                 config.EffectiveProxy(cfg),
			Namespace:             cfg.Namespace,
			NTP:                   cfg.NTP,
			ReconnectTimeout:      time.Duration(cfg.ReconnectTimeout) * time.Second,
			Watch:                 m.watchOptions(cfg),
			WatchControl:          m.watchCtrl,
			DisableAutoStartWatch: true,
			AfterConfigSave:       m.ApplyConfig,
			OnLoginSuccess:        m.onLoginSuccess,
			SetNotifier:           m.setNotifier,
			RequestReboot:         m.requestReboot,
			RequestUpdate:         m.requestUpdate,
		})

		return err
	}, rte.Recovery{MaxRestarts: 3, Delay: time.Second, Retryable: transientTransportError})
	if startErr != nil {
		m.setBotStopped(startErr.Error(), startErr)
	} else if started {
		m.mu.Lock()
		m.botStatus = moduleStatusRunning
		m.botErr = nil
		m.mu.Unlock()
	}
}

func transientTransportError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError)
}

func (m *Manager) StopBot() {
	ctx, cancel := context.WithTimeout(context.Background(), moduleStopTimeout)
	defer cancel()
	if err := m.botProcess.Stop(ctx); err != nil {
		m.mu.Lock()
		m.botStatus = err.Error()
		m.botErr = err
		m.mu.Unlock()
		return
	}
	m.setNotifier(nil)
	m.setBotStopped("stopped", nil)
}

func (m *Manager) StartWatch(ctx context.Context) error {
	m.mu.Lock()
	policyErr := m.policyErr
	m.mu.Unlock()
	if policyErr != nil {
		return policyErr
	}
	cfg := config.Get()
	if cfg == nil || (!cfg.Modules.Watch && !cfg.Modules.Forward) {
		return nil
	}
	if m.watchCtrl.Running() {
		return nil
	}
	if err := m.checkSession(ctx); err != nil {
		return err
	}
	m.watchCtrl.UpdateOptions(m.watchOptions(cfg))
	if !m.watchCtrl.Start() {
		return m.watchCtrl.LastError()
	}
	return nil
}

func (m *Manager) StopWatch() {
	m.watchCtrl.Stop()
}

func (m *Manager) restartWatch(ctx context.Context) error {
	if err := m.watchCtrl.StopContext(ctx); err != nil {
		return err
	}
	return m.StartWatch(ctx)
}

func (m *Manager) StartHTTP() {
	cfg := config.Get()
	if cfg == nil || !cfg.Modules.HTTP {
		return
	}
	m.httpCtrl.Start()
}

func (m *Manager) StopHTTP() {
	m.httpCtrl.Stop()
}

func (m *Manager) StartAria2Manager() {
	if m == nil {
		return
	}
	m.mu.Lock()
	manager, enabled := m.aria2Mgr, m.aria2Enabled
	m.mu.Unlock()
	if !enabled || manager == nil {
		return
	}
	_, err := m.aria2Process.Start(func(ctx context.Context) error {
		if m.httpService != nil {
			m.httpService.Proxy().SetTelegramFileErrorReporter(manager)
		}
		defer func() {
			if m.httpService != nil {
				m.httpService.Proxy().SetTelegramFileErrorReporter(nil)
			}
		}()
		return manager.Run(ctx)
	}, rte.Recovery{})
	m.mu.Lock()
	m.aria2Err = err
	m.mu.Unlock()
}

func (m *Manager) stopAria2(ctx context.Context) error { return m.aria2Process.Stop(ctx) }

func (m *Manager) StopAria2Manager() {
	ctx, cancel := context.WithTimeout(context.Background(), moduleStopTimeout)
	defer cancel()
	if err := m.stopAria2(ctx); err != nil {
		logctx.From(m.parent).Error("stop aria2", zap.Error(err))
	}
}

func (m *Manager) Shutdown() {
	defer func() {
		if m.accountHost != nil {
			_ = m.accountHost.Stop(context.Background())
		} else {
			_ = m.connections.Stop(context.Background())
		}
	}()
	_ = m.panelProcess.Stop(context.Background())
	_ = m.botProcess.Stop(context.Background())
	_ = m.aria2Process.Stop(context.Background())
	m.StopBot()
	m.StopAria2Manager()
	_ = m.httpCtrl.StopContext(context.Background())
	_ = m.watchCtrl.StopContext(context.Background())
	if m.downloadHost != nil {
		if err := m.downloadHost.Stop(context.Background()); err != nil {
			logctx.From(m.parent).Error("stop download control", zap.Error(err))
		}
	}
	m.mu.Lock()
	host := m.policies
	m.mu.Unlock()
	if host != nil {
		ctx, cancel := context.WithTimeout(context.Background(), moduleStopTimeout)
		defer cancel()
		if err := host.Stop(ctx); err != nil {
			logctx.From(m.parent).Error("stop policy host", zap.Error(err))
		}
	}
}

func (m *Manager) Notify(ctx context.Context, text string) {
	m.mu.Lock()
	notify := m.notify
	m.mu.Unlock()
	if notify != nil {
		notify(ctx, text)
	}
}

func (m *Manager) setNotifier(notify watch.NotifyFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notify = notify
}

func (m *Manager) onLoginSuccess(_ *tg.User) {
	cfg := config.Get()
	if cfg.Modules.Watch || cfg.Modules.Forward {
		go func() {
			if err := m.restartWatch(m.parent); err != nil {
				m.Notify(context.Background(), "登录成功，但监听服务未启动："+err.Error())
			}
		}()
	}
}

func (m *Manager) botState(cfg *config.Config) webui.ModuleState {
	m.mu.Lock()
	running := m.botProcess.Running()
	status := m.botStatus
	err := m.botErr
	m.mu.Unlock()
	if processErr := m.botProcess.LastError(); processErr != nil {
		err = processErr
	}
	if !running && err == nil && status == moduleStatusRunning {
		status = "stopped"
	}
	if cfg == nil {
		cfg = config.Get()
	}
	if status == "" {
		status = moduleStatusNotStarted
	}
	if cfg != nil && cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) == "" {
		status = "已启用，等待填写 Bot Token。"
	}
	if err != nil {
		status = err.Error()
	}
	return webui.ModuleState{
		ID:          moduleIDBot,
		Name:        "机器人控制",
		Description: "接收 Telegram 私聊命令，用于登录、配置、更新和下载任务管理。",
		Enabled:     cfg != nil && cfg.Modules.Bot,
		Running:     running,
		CanToggle:   true,
		Status:      status,
	}
}

func (m *Manager) watchState(cfg *config.Config) webui.ModuleState {
	if cfg == nil {
		cfg = config.Get()
	}
	running := m.watchCtrl.Running()
	status := moduleStatusNotStarted
	if running && cfg != nil && cfg.Modules.Watch {
		status = moduleStatusRunning
	} else if err := m.watchCtrl.LastError(); err != nil {
		status = "已停止：" + err.Error()
	} else if cfg != nil && cfg.Modules.Watch {
		status = "已启用，等待 Telegram 用户登录或启动。"
	}
	m.mu.Lock()
	policyErr := m.policyErr
	m.mu.Unlock()
	if policyErr != nil {
		status = policyErr.Error()
	}
	return webui.ModuleState{
		ID:          moduleIDWatch,
		Name:        "监听下载",
		Description: "监听 Telegram 表情触发并生成临时 HTTP 链接；是否自动提交给 aria2 由 aria2 模块的自动下载开关决定。",
		Enabled:     cfg != nil && cfg.Modules.Watch,
		Running:     running && cfg != nil && cfg.Modules.Watch,
		CanToggle:   true,
		Status:      status,
	}
}

func (m *Manager) httpState(cfg *config.Config) webui.ModuleState {
	if cfg == nil {
		cfg = config.Get()
	}
	enabled := cfg != nil && cfg.Modules.HTTP
	running := m.httpCtrl.Running()
	var status string
	if running {
		status = moduleStatusRunning + "：" + config.HTTPListenAddr(cfg)
	} else if !enabled {
		status = "已关闭"
	} else if err := m.httpCtrl.LastError(); err != nil {
		status = "已停止：" + err.Error()
	} else {
		status = "已启用，等待启动。"
	}
	return webui.ModuleState{
		ID:          moduleIDHTTP,
		Name:        "HTTP 下载代理",
		Description: "提供 /download 链接和按 DC、按文件 FIFO 调度的标准 Range 文件流；支持 aria2 及其他下载器。",
		Enabled:     enabled,
		Running:     running,
		CanToggle:   true,
		Status:      status,
	}
}

func (m *Manager) aria2State(cfg *config.Config) webui.ModuleState {
	if cfg == nil {
		cfg = config.Get()
	}
	m.mu.Lock()
	running := m.aria2Process.Running()
	err := m.aria2Err
	m.mu.Unlock()
	if processErr := m.aria2Process.LastError(); processErr != nil {
		err = processErr
	}
	enabled := cfg != nil && cfg.Modules.Aria2
	var status string
	switch {
	case !enabled:
		status = "已关闭"
	case err != nil:
		status = "已停止：" + err.Error()
	case running && watchAutoDownloadEnabled(cfg):
		status = "运行中；监听触发后自动提交到 aria2"
	case running && cfg != nil && cfg.Aria2.AutoDownload:
		status = "运行中；自动提交等待 watch 使用 aria2 模式"
	case running:
		status = "运行中；自动下载已关闭"
	default:
		status = "已启用，正在启动或连接 aria2 RPC。"
	}
	return webui.ModuleState{
		ID:          moduleIDAria2,
		Name:        "aria2 下载器管理",
		Description: "独立维护 aria2 RPC、任务恢复和异常监控；是否把监听生成的临时 HTTP 链接自动提交给 aria2 由 aria2.auto_download 控制。",
		Enabled:     enabled,
		Running:     running,
		CanToggle:   true,
		Status:      status,
	}
}

func (m *Manager) forwardState(cfg *config.Config) webui.ModuleState {
	if cfg == nil {
		cfg = config.Get()
	}
	running := m.watchCtrl.Running()
	status := moduleStatusNotStarted
	if running && cfg != nil && cfg.Modules.Forward {
		status = moduleStatusRunning
	} else if err := m.watchCtrl.LastError(); err != nil {
		status = "已停止：" + err.Error()
	} else if cfg != nil && cfg.Modules.Forward {
		status = "已启用，等待 Telegram 用户登录或启动。"
	}
	return webui.ModuleState{
		ID:          moduleIDForward,
		Name:        "监听转发",
		Description: "监听配置的 Telegram 对象，并按 forward.mode 转发到默认目标；频道会尝试自动监听关联评论区。",
		Enabled:     cfg != nil && cfg.Modules.Forward,
		Running:     running && cfg != nil && cfg.Modules.Forward,
		CanToggle:   true,
		Status:      status,
	}
}

func (m *Manager) checkSession(ctx context.Context) error {
	if m.namespaceKV == nil {
		return errors.New("本地数据未准备好")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	opts := m.sessionOptions()
	opts.Checker = m.sessionPort
	_, err := login.CheckSession(checkCtx, opts)
	return err
}

func (m *Manager) sessionOptions() login.SessionOptions {
	cfg := config.Get()
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return login.SessionOptions{
		Connections: m.connections, Credentials: m, Account: m.downloadAccount,
		KV:               m.namespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	}
}

func (m *Manager) hasRunnableModule(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return (cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) != "") || cfg.Modules.Watch || cfg.Modules.HTTP || cfg.Modules.Aria2 || cfg.Modules.Forward
}

func (m *Manager) watchOptions(cfg *config.Config) watch.Options {
	opts := watch.DefaultOptions(cfg)
	opts.Connections = m.connections
	opts.ComponentStore = m.componentStore
	opts.SetIntentHost = func(host *rte.Runtime) { m.mu.Lock(); m.intentHost = host; m.mu.Unlock() }
	opts.SetDownloadHost = func(host *rte.Runtime) { m.mu.Lock(); m.localHost = host; m.mu.Unlock() }
	m.mu.Lock()
	opts.Filter, opts.Naming = m.filter, m.naming
	host := m.policies
	m.mu.Unlock()
	opts.Credentials = m
	if host != nil {
		if value, err := host.Resolve(ports.ReactionTriggerName); err == nil {
			opts.Reaction = value.(ports.ReactionTrigger)
		}
		if value, err := host.Resolve(ports.MessageLinksName); err == nil {
			opts.MessageLinks = value.(ports.MessageLinks)
		}
	}
	opts.HTTPService = m.httpService
	opts.DownloadRouting = m
	opts.ForwardQueue = m.forwardQueue
	if cfg.Modules.Aria2 && cfg.Aria2.AutoDownload {
		m.mu.Lock()
		opts.DownloadSubmitter = m.aria2Mgr
		m.mu.Unlock()
	}
	return opts
}

func (m *Manager) setBotStopped(status string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.botStatus = status
	m.botErr = err
}

// The daemon owns policies independently of watcher reconnects and module toggles.
func newPolicyHost(ctx context.Context, cfg *config.Config) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	return newPolicyHostStored(ctx, cfg, nil)
}

func newPolicyHostStored(ctx context.Context, cfg *config.Config, store *rteconfig.Store) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	registry, err := application.Registry()
	if err != nil {
		return nil, nil, nil, err
	}
	account := types.AccountID(cfg.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	values := daemonComponentValues(cfg)
	enabled := make(map[string]bool, len(values))
	for id := range values {
		enabled[id] = true
		if store != nil {
			doc, err := store.Load(ctx, id)
			if err != nil {
				return nil, nil, nil, err
			}
			if !doc.Enabled {
				return nil, nil, nil, fmt.Errorf("required production component %s is disabled", id)
			}
			values[id], enabled[id] = doc.Values, doc.Enabled
		}
	}
	host, err := registry.Build(account, enabled, values)
	if err != nil {
		return nil, nil, nil, err
	}
	fail := func(err error) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
		_ = host.Stop(context.Background())
		return nil, nil, nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			return fail(fmt.Errorf("%s: %s", status.ID, status.Detail))
		}
	}
	filter, err := host.Resolve(ports.FilterRulesName)
	if err != nil {
		return fail(err)
	}
	naming, err := host.Resolve(ports.NamingRulesName)
	if err != nil {
		return fail(err)
	}
	return host, filter.(ports.FilterRules), naming.(ports.NamingRules), nil
}

func (m *Manager) ComponentConfigurations() ([]rte.Configuration, bool) {
	m.mu.Lock()
	policies, botHost, localHost, aria2Manager, panelHost, intentHost := m.policies, m.botComponents, m.localHost, m.aria2Mgr, m.panelHost, m.intentHost
	m.mu.Unlock()
	configurations := []rte.Configuration{}
	for _, host := range []*rte.Runtime{m.accountHost, policies, botHost, localHost, panelHost, intentHost, aria2Manager.Host(), m.httpService.Proxy().Host(), m.forwardQueue.Host(), m.downloadHost} {
		if host == nil {
			continue
		}
		for _, configuration := range host.Configurations() {
			if strings.HasPrefix(configuration.ID, "host.") {
				continue
			}
			configurations = append(configurations, configuration)
		}
	}
	return configurations, m.componentStore != nil
}

func (m *Manager) ComponentHealth() []rte.Health {
	m.mu.Lock()
	policies, botHost, localHost, aria2Manager, panelHost, intentHost := m.policies, m.botComponents, m.localHost, m.aria2Mgr, m.panelHost, m.intentHost
	m.mu.Unlock()
	result := []rte.Health{}
	for _, host := range []*rte.Runtime{m.accountHost, policies, botHost, localHost, panelHost, intentHost, aria2Manager.Host(), m.httpService.Proxy().Host(), m.forwardQueue.Host(), m.downloadHost} {
		if host != nil {
			result = append(result, host.Health())
		}
	}
	for _, process := range []*rte.Process{m.botProcess, m.aria2Process, m.panelProcess} {
		if process != nil {
			result = append(result, process.Health())
		}
	}
	if m.httpCtrl != nil {
		result = append(result, m.httpCtrl.Health())
	}
	if m.watchCtrl != nil {
		result = append(result, m.watchCtrl.Health())
	}
	return result
}

func (m *Manager) SaveComponentConfiguration(ctx context.Context, id string, values map[string]any) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if m.componentStore == nil {
		return fmt.Errorf("component configuration directory is not enabled")
	}
	m.mu.Lock()
	host := m.policies
	if id == "console.bot" || id == "notify.telegram" {
		host = m.botComponents
	}
	if id == "downloader.local" {
		host = m.localHost
	}
	if id == "downloader.aria2" {
		host = m.aria2Mgr.Host()
	}
	if id == "forwarder" {
		host = m.forwardQueue.Host()
	}
	if id == "proxy.range" {
		host = m.httpService.Proxy().Host()
	}
	if id == "download.control" {
		host = m.downloadHost
	}
	m.mu.Unlock()
	if host == nil {
		return fmt.Errorf("component host is unavailable")
	}
	return host.PatchSaved(ctx, id, values, m.componentStore)
}

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
	"github.com/snakexgc/tdl/app/reset"
	"github.com/snakexgc/tdl/app/updater"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/app/webui"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/componentconfig"
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
	ResetPlan          *reset.Plan
	RequestReset       func()
	RequestReboot      func()
	RequestUpdate      func(updater.Plan)
}

type aria2ManagerConfig struct {
	RPCURL         string
	Secret         string
	TimeoutSeconds int
}

func effectiveAria2ManagerConfig(cfg *config.Config) aria2ManagerConfig {
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return aria2ManagerConfig{
		RPCURL:         cfg.Aria2.RPCURL,
		Secret:         cfg.Aria2.Secret,
		TimeoutSeconds: cfg.Aria2.TimeoutSeconds,
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
	transitionWG     sync.WaitGroup
	scheduleMu       sync.Mutex
	closing          atomic.Bool
	reconciler       *rte.Reconciler
	configSource     *config.Source
	configured       map[string]bool
	configurationErr error
	directory        *rte.Directory
	accountHost      *rte.Runtime
	sessionPort      ports.AccountSession
	connections      *tgauth.Connections
	intentHost       *rte.Runtime
	botProcess       *rte.Process
	aria2Process     *rte.Process
	panelProcess     *rte.Process
	panelHost        *rte.Runtime
	localHost        *rte.Runtime
	botComponents    *rte.Runtime
	botRefresh       func(context.Context) error
	componentStore   *rteconfig.Store
	policyErr        error
	policies         *rte.Runtime
	filter           ports.FilterRules
	naming           ports.NamingRules
	parent           context.Context
	forwardQueue     *appforward.Queue
	downloadAccount  types.AccountID
	downloadHost     *rte.Runtime
	downloadPort     ports.DownloadControl

	kvEngine    kv.Storage
	namespaceKV storage.Storage
	watchCtrl   *watch.Controller
	httpService *httpdl.Service
	httpCtrl    *httpdl.Controller
	aria2Mgr    *aria2.Manager
	aria2Err    error
	aria2Config aria2ManagerConfig

	requestReboot func()
	resetPlan     *reset.Plan
	requestReset  func()
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
	if opts.RequestReset != nil {
		opts.RequestReset = wrapShutdown(cancel, opts.RequestReset)
	}
	opts.RequestUpdate = wrapUpdateShutdown(cancel, opts.RequestUpdate)

	manager := NewManager(runCtx, engine, namespaceKV, opts)
	if manager.configurationErr != nil {
		manager.Shutdown()
		return manager.configurationErr
	}
	webStarted := manager.StartWebUI(runCtx)
	manager.ApplyConfig(config.Get())

	if !webStarted && !manager.hasRunnableModule(config.From(manager.parent)) {
		manager.Shutdown()
		return errors.New("please configure webui.address, webui.port, webui.username and webui.password, or configure bot.token")
	}

	<-runCtx.Done()
	return manager.shutdown()
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
	cfg := config.From(ctx)
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	var componentStore *rteconfig.Store
	if opts.ComponentConfigDir != "" {
		componentStore = rteconfig.NewStore(opts.ComponentConfigDir)
	}
	effective, enabled, configErr := componentconfig.Load(ctx, componentStore, cfg)
	if configErr == nil {
		cfg = effective
	}
	source := config.NewSource(cfg)
	ctx = config.WithSource(ctx, source)
	manager := &Manager{
		configSource: source, configured: enabled, configurationErr: configErr,
		downloadAccount: types.AccountID(cfg.Namespace),
		componentStore:  componentStore,
		policyErr:       configErr,
		parent:          ctx,
		forwardQueue:    appforward.NewQueue(namespaceKV),
		kvEngine:        engine,
		namespaceKV:     namespaceKV,
		requestReboot:   opts.RequestReboot,
		resetPlan:       opts.ResetPlan,
		requestReset:    opts.RequestReset,
		requestUpdate:   opts.RequestUpdate,
		botStatus:       moduleStatusNotStarted,
		watchMode:       config.EffectiveDownloaderMode(cfg),
		watchEnabled:    cfg.Modules.Watch,
		forwardEnabled:  cfg.Modules.Forward,
		aria2Enabled:    cfg.Modules.Aria2,
		aria2Auto:       watchAutoDownloadEnabled(cfg),
		aria2Config:     effectiveAria2ManagerConfig(cfg),
	}
	if manager.downloadAccount == "" {
		manager.downloadAccount = types.DefaultAccount
	}
	manager.reconciler = rte.NewReconciler(manager.downloadAccount)
	if err := manager.reconciler.Reconcile(ctx, manager.foundationUnits(cfg)); err != nil {
		logctx.From(ctx).Error("initialize component resources", zap.Error(err))
	}
	manager.botProcess = rte.NewProcess(ctx, manager.downloadAccount, "host.bot")
	manager.aria2Process = rte.NewProcess(ctx, manager.downloadAccount, "host.aria2")
	manager.panelProcess = rte.NewProcess(ctx, manager.downloadAccount, "host.panel")
	manager.forwardQueue.SetNotifier(manager.Notify)
	manager.httpService = httpdl.NewService(cfg, namespaceKV, logctx.From(ctx))
	manager.httpService.Proxy().SetComponentStore(manager.componentStore)
	manager.httpCtrl = httpdl.NewController(ctx, manager.httpService)
	manager.aria2Mgr = aria2.NewManager(cfg, namespaceKV, logctx.From(ctx), engine)
	manager.aria2Mgr.SetComponentStore(manager.componentStore)
	manager.watchCtrl = watch.NewController(ctx, manager.watchOptions(cfg), manager.Notify)
	manager.policyErr = errors.Join(manager.policyErr, manager.initDirectory())
	return manager
}

func (m *Manager) StartWebUI(ctx context.Context) bool {
	if m.configurationErr != nil || !m.componentEnabled("panel.webui") {
		return false
	}
	cfg := config.From(m.parent)
	if cfg == nil || !cfg.Modules.WebUI || strings.TrimSpace(config.WebUIListenAddr(cfg)) == "" {
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
			ComponentStore:   m.componentStore,
			SetComponentHost: func(host *rte.Runtime) { m.mu.Lock(); m.panelHost = host; m.mu.Unlock() },
			Connections:      m.connections,
			SessionChecker:   m.sessionPort,
			Credentials:      m, Updater: m,
			DownloadControl:  m,
			LocalLinks:       savedLocalLinks{manager: m},
			KVEngine:         m.kvEngine,
			ForwardQueue:     m.forwardQueue,
			Namespace:        cfg.Namespace,
			NamespaceKV:      m.namespaceKV,
			AfterConfigSave:  m.ApplyConfig,
			OnLoginSuccess:   m.onLoginSuccess,
			RequestReboot:    m.requestReboot,
			ResetPlan:        m.resetPlan,
			RequestReset:     m.requestReset,
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
		cfg = config.From(m.parent)
	}
	if cfg == nil {
		return
	}
	if m.configSource != nil {
		effective, enabled, err := componentconfig.Load(m.parent, m.componentStore, cfg)
		if err != nil {
			logctx.From(m.parent).Error("load component configuration", zap.Error(err))
			return
		}
		cfg = effective
		m.configSource.Replace(cfg)
		m.mu.Lock()
		m.configured = enabled
		m.mu.Unlock()
	}
	version := m.applyVersion.Add(1)
	if err := m.applyConfigLocked(cfg, version, true); err != nil {
		logctx.From(m.parent).Error("apply configuration failed", zap.Error(err))
	}
}

// applyConfigLocked reconciles every managed module against one config
// generation. The caller must hold applyMu. Background stops are generation
// checked and serialized with starts, so an older ApplyConfig cannot stop a
// module that a newer config has already enabled.
func (m *Manager) applyConfigLocked(cfg *config.Config, version uint64, async bool) error {
	m.mu.Lock()
	m.watchMode = config.EffectiveDownloaderMode(cfg)
	m.watchEnabled, m.forwardEnabled = cfg.Modules.Watch, cfg.Modules.Forward
	m.aria2Enabled, m.aria2Auto = cfg.Modules.Aria2, watchAutoDownloadEnabled(cfg)
	m.mu.Unlock()
	m.httpService.UpdateConfig(cfg)
	units := m.managedUnits(cfg)
	apply := func() error {
		result := m.reconciler.Reconcile(m.parent, units)
		m.mu.Lock()
		hosts := []*rte.Runtime{m.localHost, m.intentHost}
		m.mu.Unlock()
		if m.forwardQueue != nil {
			hosts = append(hosts, m.forwardQueue.Host())
		}
		for _, host := range hosts {
			if host != nil {
				result = errors.Join(result, host.ReconcileSaved(m.parent, m.componentStore))
			}
		}
		if result != nil {
			return result
		}
		if m.directory != nil {
			return m.directory.ApplyPending(m.parent)
		}
		return nil
	}
	if async {
		m.runTransition(version, func() {
			if err := apply(); err != nil {
				logctx.From(m.parent).Error("reconcile components", zap.Error(err))
			}
		})
		return nil
	}
	var err error
	m.transition(version, func() { err = apply() })
	return err
}

func (m *Manager) transition(version uint64, fn func()) bool {
	if fn == nil {
		return false
	}
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	if m.closing.Load() || m.applyVersion.Load() != version {
		return false
	}
	fn()
	return true
}

func (m *Manager) runTransition(version uint64, fn func()) {
	m.scheduleMu.Lock()
	defer m.scheduleMu.Unlock()
	if m.closing.Load() {
		return
	}
	m.transitionWG.Add(1)
	go func() { defer m.transitionWG.Done(); m.transition(version, fn) }()
}

func (m *Manager) StartBot() {
	cfg := config.From(m.parent)
	if cfg == nil || !cfg.Modules.Bot {
		return
	}
	if strings.TrimSpace(cfg.Bot.Token) == "" {
		m.setBotStopped("未启动：请先填写 Telegram Bot Token。", nil)
		return
	}

	started, startErr := m.botProcess.Start(func(ctx context.Context) error {
		err := bot.Run(ctx, bot.Options{
			CommandResolver: m.ResolveComponentPort,
			DownloadControl: m,
			Connections:     m.connections,
			SessionChecker:  m.sessionPort,
			Credentials:     m, Updater: m,
			ComponentStore:        m.componentStore,
			SetComponentHost:      func(host *rte.Runtime) { m.mu.Lock(); m.botComponents = host; m.mu.Unlock() },
			SetComponentRefresh:   func(refresh func(context.Context) error) { m.mu.Lock(); m.botRefresh = refresh; m.mu.Unlock() },
			Token:                 cfg.Bot.Token,
			ForwardQueue:          m.forwardQueue,
			AllowedUsers:          cfg.Bot.AllowedUsers,
			Proxy:                 m.botProxy(cfg),
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
	if m.configurationErr != nil {
		return m.configurationErr
	}
	cfg := config.From(m.parent)
	if !m.connectionNeeded(cfg) {
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
	cfg := config.From(m.parent)
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

func (m *Manager) Shutdown() { _ = m.shutdown() }

func (m *Manager) shutdown() error {
	m.scheduleMu.Lock()
	m.closing.Store(true)
	m.applyVersion.Add(1)
	m.scheduleMu.Unlock()
	m.transitionWG.Wait()
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	units := m.managedUnits(config.From(m.parent))
	for i := range units {
		units[i].Enabled = false
	}
	if err := m.reconciler.Reconcile(context.Background(), units); err != nil {
		logctx.From(m.parent).Error("stop component resources; instances retained for retry", zap.Error(err))
		return err
	}
	return nil
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
	cfg := config.From(m.parent)
	if m.connectionNeeded(cfg) {
		m.runTransition(m.applyVersion.Load(), func() {
			if err := m.restartWatch(m.parent); err != nil {
				m.Notify(context.Background(), "登录成功，但监听服务未启动："+err.Error())
			}
		})
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
		cfg = config.From(m.parent)
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
		cfg = config.From(m.parent)
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
		cfg = config.From(m.parent)
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
		cfg = config.From(m.parent)
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
		cfg = config.From(m.parent)
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
	cfg := config.From(m.parent)
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
	return (cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) != "") || cfg.Modules.HTTP || cfg.Modules.Aria2 || m.connectionNeeded(cfg)
}

func (m *Manager) watchOptions(cfg *config.Config) watch.Options {
	opts := watch.DefaultOptions(cfg)
	opts.Connections = m.connections
	opts.ComponentStore = m.componentStore
	opts.SetIntentHost = func(host *rte.Runtime) { m.mu.Lock(); m.intentHost = host; m.mu.Unlock() }
	opts.SetDownloadHost = func(host *rte.Runtime) { m.mu.Lock(); m.localHost = host; m.mu.Unlock() }
	opts.Filter, opts.Naming = filterPort{m}, namingPort{m}
	opts.FeatureFlags = func() (bool, bool) {
		current := config.From(m.parent)
		return current.Modules.Watch, current.Modules.Forward
	}
	opts.ForwardConfig = func() watch.ForwardSettings {
		return watch.DefaultOptions(config.From(m.parent)).ForwardSettings()
	}
	opts.Credentials = m
	opts.Reaction = reactionPort{m}
	opts.MessageLinks = messageLinksPort{m}
	opts.HTTPService = m.httpService
	opts.DownloadRouting = m
	opts.DownloadPipeline = m
	opts.ForwardQueue = m.forwardQueue
	opts.ForwardRules = m
	opts.DownloadSubmitter = aria2Submission{manager: m}
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
	catalog, err := application.Catalog()
	if err != nil {
		return nil, nil, nil, err
	}
	return newCatalogPolicyHost(ctx, cfg, store, catalog)
}

func buildCatalogPolicyHost(ctx context.Context, cfg *config.Config, store *rteconfig.Store, catalog *rte.Catalog) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	account := types.AccountID(cfg.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	values := daemonComponentValues(cfg)
	enabled := make(map[string]bool, len(values))
	for _, definition := range catalog.Definitions() {
		if definition.Factory == nil || definition.Host != "" {
			continue
		}
		if err := registry.Register(definition.Manifest, definition.Factory); err != nil {
			return nil, err
		}
		id := definition.Manifest.ID
		enabled[id] = true
		if store != nil {
			doc, err := store.Load(ctx, id)
			if err != nil {
				return nil, err
			}
			values[id], enabled[id] = doc.Values, doc.Enabled
		}
	}
	host, err := registry.Build(account, enabled, values)
	if err != nil {
		return nil, err
	}
	return host, nil
}

func newCatalogPolicyHost(ctx context.Context, cfg *config.Config, store *rteconfig.Store, catalog *rte.Catalog) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	host, err := buildCatalogPolicyHost(ctx, cfg, store, catalog)
	if err != nil {
		return nil, nil, nil, err
	}
	host.Start(ctx)
	filterValue, filterErr := host.Resolve(ports.FilterRulesName)
	namingValue, namingErr := host.Resolve(ports.NamingRulesName)
	filter, _ := filterValue.(ports.FilterRules)
	naming, _ := namingValue.(ports.NamingRules)
	return host, filter, naming, errors.Join(filterErr, namingErr)
}

func (m *Manager) ComponentConfigurations() ([]rte.Configuration, bool) {
	if m.directory == nil {
		return nil, false
	}
	return m.directory.Configurations(context.Background()), m.componentStore != nil
}

func (m *Manager) ComponentHealth() []rte.Health {
	if m.directory == nil {
		return nil
	}
	return m.directory.Health()
}

func (m *Manager) SaveComponentConfiguration(ctx context.Context, id string, values map[string]any) error {
	return m.SaveComponentConfigurationVersion(ctx, id, values, "")
}

func (m *Manager) SaveComponentConfigurationVersion(ctx context.Context, id string, values map[string]any, revision string) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if m.directory == nil {
		return fmt.Errorf("component directory is unavailable")
	}
	if err := m.directory.PatchWithRevision(ctx, id, values, revision); err != nil {
		return err
	}
	if m.configSource == nil {
		return nil
	}
	cfg, enabled, err := componentconfig.Load(ctx, m.componentStore, config.From(m.parent))
	if err != nil {
		return err
	}
	m.configSource.Replace(cfg)
	m.mu.Lock()
	m.configured = enabled
	m.mu.Unlock()
	return m.applyConfigLocked(cfg, m.applyVersion.Add(1), true)
}

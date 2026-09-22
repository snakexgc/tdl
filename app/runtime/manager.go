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
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/app/webui"
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	ntpclient "github.com/snakexgc/tdl/bsw/ecual/ntp"
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
	moduleStopTimeout = 10 * time.Second
	moduleIDBot       = "bot"
	moduleIDWatch     = "watch"
	moduleIDAria2     = "aria2"
)

type Options struct {
	ComponentStore    *rteconfig.Store
	ConfigurationHost *rte.Runtime
	TimeProbe         ports.TimeProbe
	ResetPlan         *reset.Plan
	RequestReset      func()
	RequestReboot     func()
	RequestUpdate     func(types.UpdatePlan)
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
		config.UsesDownloadExecutor(cfg, config.DownloadExecutorAria2)
}

type Manager struct {
	transitionWG      sync.WaitGroup
	scheduleMu        sync.Mutex
	closing           atomic.Bool
	reconciler        *rte.Reconciler
	configSource      *config.Source
	configured        map[string]bool
	configurationErr  error
	directory         *rte.Directory
	accountHost       *rte.Runtime
	sessionPort       ports.AccountSession
	connections       *tgauth.Connections
	intentHost        *rte.Runtime
	botProcess        *rte.Process
	aria2Process      *rte.Process
	panelProcess      *rte.Process
	timeHost          *rte.Runtime
	clock             *rte.ClockBinding
	timeProbe         ports.TimeProbe
	panelHost         *rte.Runtime
	localHost         *rte.Runtime
	botComponents     *rte.Runtime
	botRefresh        func(context.Context) error
	componentStore    *rteconfig.Store
	savedStore        *rteconfig.Store
	configurationHost *rte.Runtime
	policyErr         error
	policies          *rte.Runtime
	filter            ports.FilterRules
	naming            ports.NamingRules
	parent            context.Context
	forwardQueue      *appforward.Queue
	downloadAccount   types.AccountID
	downloadHost      *rte.Runtime
	downloadPort      ports.DownloadControl

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
	requestUpdate func(types.UpdatePlan)

	applyMu        sync.Mutex
	transitionMu   sync.Mutex
	applyVersion   atomic.Uint64
	mu             sync.Mutex
	notify         watch.NotifyFunc
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
		logctx.From(runCtx).Error("启动配置无效", zap.Error(manager.configurationErr))
		manager.Shutdown()
		return manager.configurationErr
	}
	manager.StartHTTP()
	webStarted := manager.StartWebUI(runCtx)
	if runCtx.Err() != nil {
		return manager.shutdown()
	}
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

func wrapUpdateShutdown(cancel context.CancelFunc, fn func(types.UpdatePlan)) func(types.UpdatePlan) {
	return func(plan types.UpdatePlan) {
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
	componentStore, snapshotErr := startupStore(ctx, opts.ComponentStore)
	effective, enabled, configErr := config.Load(ctx, componentStore, config.System(cfg))
	configErr = errors.Join(snapshotErr, configErr)
	if configErr == nil {
		cfg = effective
	}
	source := config.NewSource(cfg)
	ctx = config.WithSource(ctx, source)
	clock := new(rte.ClockBinding)
	ctx = rte.WithClock(ctx, clock)
	if opts.TimeProbe == nil {
		opts.TimeProbe = ntpclient.Client{}
	}
	manager := &Manager{
		clock: clock, timeProbe: opts.TimeProbe,
		configSource: source, configured: enabled, configurationErr: configErr,
		downloadAccount:   types.AccountID(cfg.Namespace),
		componentStore:    componentStore,
		savedStore:        opts.ComponentStore,
		configurationHost: opts.ConfigurationHost,
		policyErr:         configErr,
		parent:            ctx,
		forwardQueue:      appforward.NewQueue(namespaceKV),
		kvEngine:          engine,
		namespaceKV:       namespaceKV,
		requestReboot:     opts.RequestReboot,
		resetPlan:         opts.ResetPlan,
		requestReset:      opts.RequestReset,
		requestUpdate:     opts.RequestUpdate,
		watchEnabled:      cfg.Modules.Watch,
		forwardEnabled:    cfg.Modules.Forward,
		aria2Enabled:      config.Aria2Enabled(cfg),
		aria2Auto:         watchAutoDownloadEnabled(cfg),
		aria2Config:       effectiveAria2ManagerConfig(cfg),
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
	if m.configurationErr != nil || !m.componentEnabled(panelComponentID) {
		return false
	}
	cfg := config.From(m.parent)
	if cfg == nil || !cfg.Modules.WebUI || strings.TrimSpace(config.WebUIListenAddr(cfg)) == "" {
		logctx.From(ctx).Warn("Web 管理面板未启动：监听地址未配置", zap.String("component", panelComponentID))
		color.Yellow("Web 管理面板未启动：webui.address 或 webui.port 为空。")
		return false
	}
	if strings.TrimSpace(cfg.WebUI.Username) == "" || cfg.WebUI.Password == "" {
		logctx.From(ctx).Warn("Web 管理面板未启动：登录凭据未配置", zap.String("component", panelComponentID))
		color.Yellow("Web 管理面板未启动：请设置 webui.username 和 webui.password。")
		return false
	}

	errCh := make(chan error, 1)
	ready := make(chan struct{})
	started, startErr := m.panelProcess.Start(func(ctx context.Context) error {
		// Also unblock startup if Process recovers a panic in the panel adapter.
		defer close(errCh)
		err := webui.Run(ctx, webui.Options{
			Ready:            func() { close(ready) },
			ComponentStore:   m.componentStore,
			SetComponentHost: func(host *rte.Runtime) { m.mu.Lock(); m.panelHost = host; m.mu.Unlock() },
			Connections:      m.connections,
			SessionChecker:   m.sessionPort,
			Credentials:      m, Updater: m,
			DownloadControl:      m,
			LocalLinks:           savedLocalLinks{manager: m},
			KVEngine:             m.kvEngine,
			ForwardQueue:         m.forwardQueue,
			Namespace:            cfg.Namespace,
			NamespaceKV:          m.namespaceKV,
			ConfigurationManager: m,
			OnLoginSuccess:       m.onLoginSuccess,
			RequestReboot:        m.requestReboot,
			ResetPlan:            m.resetPlan,
			RequestReset:         m.requestReset,
			RequestUpdate:        m.requestUpdate,
			WatchRunning:         m.watchCtrl.Running,
			ComponentManager:     m,
		})
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			logctx.From(ctx).Error("Web 管理面板异常停止", zap.String("component", panelComponentID), zap.Error(err))
		}
		errCh <- err
		return err
	}, rte.Recovery{})
	if startErr != nil {
		logctx.From(ctx).Error("启动 Web 管理面板失败", zap.String("component", panelComponentID), zap.Error(startErr))
		return false
	}
	if !started {
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.panelHost != nil && m.panelProcess.Running()
	}

	select {
	case <-errCh:
		return false
	case <-ctx.Done():
		return false
	case <-m.parent.Done():
		return false
	case <-ready:
	}
	logctx.From(ctx).Info("Web 管理面板已启动", zap.String("component", panelComponentID), zap.String("listen_addr", config.WebUIListenAddr(cfg)))
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
		effective, enabled, err := config.Load(m.parent, m.componentStore, config.System(cfg))
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
	m.watchEnabled, m.forwardEnabled = cfg.Modules.Watch, cfg.Modules.Forward
	m.aria2Enabled, m.aria2Auto = config.Aria2Enabled(cfg), watchAutoDownloadEnabled(cfg)
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
		return
	}

	_, _ = m.botProcess.Start(func(ctx context.Context) error {
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
			AllowedUsers:          cfg.Bot.AllowedUsers,
			Proxy:                 m.botProxy(cfg),
			Namespace:             cfg.Namespace,
			ReconnectTimeout:      time.Duration(cfg.ReconnectTimeout) * time.Second,
			WatchControl:          m.watchCtrl,
			DisableAutoStartWatch: true,
			OnLoginSuccess:        m.onLoginSuccess,
			SetNotifier:           m.setNotifier,
			RequestReboot:         m.requestReboot,
			RequestUpdate:         m.requestUpdate,
		})

		return err
	}, rte.Recovery{MaxRestarts: 3, Delay: time.Second, Retryable: transientTransportError})
}

func transientTransportError(err error) bool {
	var networkError net.Error
	return errors.As(err, &networkError)
}

func (m *Manager) StopBot() {
	ctx, cancel := context.WithTimeout(context.Background(), moduleStopTimeout)
	defer cancel()
	if err := m.botProcess.Stop(ctx); err != nil {
		return
	}
	m.setNotifier(nil)
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
	// watch.Run owns authentication and reconnect attempts. An online preflight
	// here would hold the lifecycle transition and prevent recovery when offline.
	if err := ctx.Err(); err != nil {
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
	first := !m.closing.Swap(true)
	m.applyVersion.Add(1)
	m.scheduleMu.Unlock()
	if first {
		color.Yellow("⏹ 正在停止 TDL：保存任务状态并关闭服务…")
		logctx.From(m.parent).Info("TDL 正在停止")
	}
	m.transitionWG.Wait()
	m.transitionMu.Lock()
	defer m.transitionMu.Unlock()
	units := m.managedUnits(config.From(m.parent))
	for i := range units {
		units[i].Enabled = false
	}
	if err := m.reconciler.Reconcile(context.WithoutCancel(m.parent), units); err != nil {
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

func (m *Manager) sessionOptions() login.SessionOptions {
	cfg := config.From(m.parent)
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	return login.SessionOptions{
		Connections: m.connections, Credentials: m, Account: m.downloadAccount,
		KV:               m.namespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	}
}

func (m *Manager) hasRunnableModule(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return (cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) != "") || cfg.Modules.HTTP || cfg.Modules.Aria2 || m.connectionNeeded(cfg) || m.componentEnabled("time.sync")
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

// The daemon owns policies independently of watcher reconnects and module toggles.
func newPolicyHostStored(ctx context.Context, cfg *config.Config, store *rteconfig.Store) (*rte.Runtime, ports.FilterRules, ports.NamingRules, error) {
	catalog, err := application.Catalog()
	if err != nil {
		return nil, nil, nil, err
	}
	return newCatalogPolicyHost(ctx, cfg, store, catalog)
}

func buildCatalogPolicyHost(ctx context.Context, cfg *config.Config, store *rteconfig.Store, catalog *rte.Catalog) (*rte.Runtime, error) {
	if store == nil {
		return nil, fmt.Errorf("component configuration store is required")
	}
	registry := rte.NewRegistry()
	account := types.AccountID(cfg.Namespace)
	if account == "" {
		account = types.DefaultAccount
	}
	values := map[string]map[string]any{}
	enabled := map[string]bool{}
	for _, definition := range catalog.Definitions() {
		if definition.Factory == nil || definition.Host != "" {
			continue
		}
		if err := registry.Register(definition.Manifest, definition.Factory); err != nil {
			return nil, err
		}
		id := definition.Manifest.ID
		doc, err := store.Load(ctx, id)
		if err != nil {
			return nil, err
		}
		values[id], enabled[id] = doc.Values, doc.Enabled
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
	return m.directory.Configurations(context.Background()), m.savedStore != nil
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
	return m.directory.PatchWithRevision(ctx, id, values, revision)
}

package runtime

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/app/bot"
	appforward "github.com/snakexgc/tdl/app/forward"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/app/login"
	"github.com/snakexgc/tdl/app/updater"
	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/app/webui"
	"github.com/snakexgc/tdl/core/logctx"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
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
	RequestReboot func()
	RequestUpdate func(updater.Plan)
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
	parent context.Context

	kvEngine    kv.Storage
	namespaceKV storage.Storage
	watchCtrl   *watch.Controller
	httpService *httpdl.Service
	httpCtrl    *httpdl.Controller
	aria2Mgr    *aria2.Manager
	aria2Cancel context.CancelFunc
	aria2Done   chan struct{}
	aria2Err    error
	aria2Config aria2ManagerConfig

	requestReboot func()
	requestUpdate func(updater.Plan)

	applyMu        sync.Mutex
	transitionMu   sync.Mutex
	applyVersion   atomic.Uint64
	mu             sync.Mutex
	notify         watch.NotifyFunc
	botCancel      context.CancelFunc
	botDone        chan struct{}
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

	// Bind the persistent forward queue to the namespace KV so the bot, watcher
	// worker and WebUI all share one durable, single-flight queue.
	appforward.ConfigureQueue(namespaceKV)

	opts.RequestReboot = wrapShutdown(cancel, opts.RequestReboot)
	opts.RequestUpdate = wrapUpdateShutdown(cancel, opts.RequestUpdate)

	manager := NewManager(runCtx, engine, namespaceKV, opts)
	appforward.Jobs().SetNotifier(manager.Notify)
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
	manager := &Manager{
		parent:         ctx,
		kvEngine:       engine,
		namespaceKV:    namespaceKV,
		requestReboot:  opts.RequestReboot,
		requestUpdate:  opts.RequestUpdate,
		botStatus:      moduleStatusNotStarted,
		watchMode:      config.EffectiveDownloaderMode(cfg),
		watchEnabled:   cfg != nil && cfg.Modules.Watch,
		forwardEnabled: cfg != nil && cfg.Modules.Forward,
		aria2Enabled:   cfg != nil && cfg.Modules.Aria2,
		aria2Auto:      watchAutoDownloadEnabled(cfg),
		aria2Config:    effectiveAria2ManagerConfig(cfg),
	}
	manager.httpService = httpdl.NewService(cfg, namespaceKV, logctx.From(ctx))
	manager.httpCtrl = httpdl.NewController(ctx, manager.httpService)
	manager.aria2Mgr = aria2.NewManager(cfg, namespaceKV, logctx.From(ctx))
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
	go func() {
		err := webui.Run(ctx, webui.Options{
			KVEngine:        m.kvEngine,
			Namespace:       cfg.Namespace,
			NamespaceKV:     m.namespaceKV,
			AfterConfigSave: m.ApplyConfig,
			OnLoginSuccess:  m.onLoginSuccess,
			RequestReboot:   m.requestReboot,
			RequestUpdate:   m.requestUpdate,
			WatchRunning:    m.watchCtrl.Running,
			ModuleManager:   m,
		})
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, context.Canceled) {
			color.Yellow("WebUI stopped: %v", err)
		}
		errCh <- err
	}()

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
	_ = m.applyConfigLocked(cfg, version, true, context.Background())
}

// applyConfigLocked reconciles every managed module against one config
// generation. The caller must hold applyMu. Background stops are generation
// checked and serialized with starts, so an older ApplyConfig cannot stop a
// module that a newer config has already enabled.
func (m *Manager) applyConfigLocked(cfg *config.Config, version uint64, async bool, watchCtx context.Context) error {
	if watchCtx == nil {
		watchCtx = context.Background()
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
		m.transition(version, func() {
			m.StopAria2Manager()
			manager := aria2.NewManager(cfg, m.namespaceKV, logctx.From(m.parent))
			m.mu.Lock()
			m.aria2Mgr = manager
			m.aria2Config = nextAria2Config
			m.mu.Unlock()
		})
	}
	m.watchCtrl.UpdateOptions(m.watchOptions(cfg))
	restartWatch := m.watchCtrl.Running() &&
		((prevWatchMode != "" && prevWatchMode != nextWatchMode) ||
			prevWatchEnabled != cfg.Modules.Watch ||
			prevForwardEnabled != cfg.Modules.Forward ||
			prevAria2Auto != nextAria2Auto ||
			(aria2ConfigChanged && nextAria2Auto))

	if cfg.Modules.HTTP {
		m.transition(version, func() {
			if restartHTTP && m.httpCtrl.Running() {
				m.StopHTTP()
			}
			m.StartHTTP()
		})
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
					m.StopWatch()
					_ = m.StartWatch(watchCtx)
				})
			} else {
				var err error
				m.transition(version, func() {
					m.StopWatch()
					err = m.StartWatch(watchCtx)
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

	m.mu.Lock()
	if m.botCancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	m.botCancel = cancel
	m.botDone = done
	m.botStatus = "启动中"
	m.botErr = nil
	m.mu.Unlock()

	go func() {
		err := bot.Run(ctx, bot.Options{
			Token:                 cfg.Bot.Token,
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

		m.mu.Lock()
		if m.botDone == done {
			m.botCancel = nil
			m.botDone = nil
			if err != nil && !errors.Is(err, context.Canceled) {
				m.botErr = err
				m.botStatus = "已停止：" + err.Error()
			} else {
				m.botErr = nil
				m.botStatus = "已停止"
			}
		}
		m.mu.Unlock()
		close(done)
	}()
}

func (m *Manager) StopBot() {
	m.mu.Lock()
	cancel := m.botCancel
	done := m.botDone
	m.botStatus = "正在停止"
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		timer := time.NewTimer(moduleStopTimeout)
		select {
		case <-done:
		case <-timer.C:
			m.mu.Lock()
			m.botStatus = "停止超时"
			m.mu.Unlock()
		}
		timer.Stop()
	}
	m.setNotifier(nil)
	if cancel == nil {
		m.setBotStopped("已停止", nil)
	}
}

func (m *Manager) StartWatch(ctx context.Context) error {
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
	m.watchCtrl.Start()
	return nil
}

func (m *Manager) StopWatch() {
	m.watchCtrl.Stop()
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
	manager := m.aria2Mgr
	if !m.aria2Enabled || manager == nil || m.aria2Cancel != nil {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.parent)
	done := make(chan struct{})
	m.aria2Cancel = cancel
	m.aria2Done = done
	m.aria2Err = nil
	m.mu.Unlock()

	if m.httpService != nil && m.httpService.Proxy() != nil {
		m.httpService.Proxy().SetTelegramFileErrorReporter(manager)
	}
	go func() {
		err := manager.Run(ctx)
		m.mu.Lock()
		if m.aria2Done == done {
			m.aria2Cancel = nil
			m.aria2Done = nil
			m.aria2Err = err
		}
		m.mu.Unlock()
		close(done)
	}()
}

func (m *Manager) StopAria2Manager() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.aria2Cancel
	done := m.aria2Done
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		timer := time.NewTimer(moduleStopTimeout)
		select {
		case <-done:
		case <-timer.C:
		}
		timer.Stop()
	}
	if m.httpService != nil && m.httpService.Proxy() != nil {
		m.httpService.Proxy().SetTelegramFileErrorReporter(nil)
	}
}

func (m *Manager) Shutdown() {
	m.StopBot()
	m.StopAria2Manager()
	m.StopHTTP()
	m.StopWatch()
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
			if err := m.StartWatch(context.Background()); err != nil {
				m.Notify(context.Background(), "登录成功，但监听服务未启动："+err.Error())
			}
		}()
	}
}

func (m *Manager) botState(cfg *config.Config) webui.ModuleState {
	m.mu.Lock()
	running := m.botCancel != nil
	status := m.botStatus
	err := m.botErr
	m.mu.Unlock()
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
	running := m.aria2Cancel != nil
	err := m.aria2Err
	m.mu.Unlock()
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
	cfg := config.Get()
	checkCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	_, err := login.CheckSession(checkCtx, login.SessionOptions{
		KV:               m.namespaceKV,
		Proxy:            config.EffectiveProxy(cfg),
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	})
	return err
}

func (m *Manager) hasRunnableModule(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return (cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) != "") || cfg.Modules.Watch || cfg.Modules.HTTP || cfg.Modules.Aria2 || cfg.Modules.Forward
}

func (m *Manager) watchOptions(cfg *config.Config) watch.Options {
	opts := watch.DefaultOptions(cfg)
	opts.HTTPService = m.httpService
	if watchAutoDownloadEnabled(cfg) {
		m.mu.Lock()
		opts.DownloadSubmitter = m.aria2Mgr
		m.mu.Unlock()
	}
	return opts
}

func (m *Manager) setBotStopped(status string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.botCancel = nil
	m.botDone = nil
	m.botStatus = status
	m.botErr = err
}

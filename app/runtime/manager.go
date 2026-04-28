package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/app/bot"
	"github.com/iyear/tdl/app/login"
	"github.com/iyear/tdl/app/updater"
	"github.com/iyear/tdl/app/watch"
	"github.com/iyear/tdl/app/webui"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/kv"
)

const moduleStopTimeout = 10 * time.Second

type Options struct {
	RequestReboot func()
	RequestUpdate func(updater.Plan)
}

type Manager struct {
	parent context.Context

	kvEngine    kv.Storage
	namespaceKV storage.Storage
	watchCtrl   *watch.Controller

	requestReboot func()
	requestUpdate func(updater.Plan)

	mu        sync.Mutex
	notify    watch.NotifyFunc
	botCancel context.CancelFunc
	botDone   chan struct{}
	botStatus string
	botErr    error
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
		return errors.New("please configure webui.listen, webui.username and webui.password, or configure bot.token")
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
	manager := &Manager{
		parent:        ctx,
		kvEngine:      engine,
		namespaceKV:   namespaceKV,
		requestReboot: opts.RequestReboot,
		requestUpdate: opts.RequestUpdate,
		botStatus:     "未启动",
	}
	manager.watchCtrl = watch.NewController(ctx, watch.DefaultOptions(config.Get()), manager.Notify)
	return manager
}

func (m *Manager) StartWebUI(ctx context.Context) bool {
	cfg := config.Get()
	if cfg == nil || strings.TrimSpace(cfg.WebUI.Listen) == "" {
		color.Yellow("Web 管理面板未启动：webui.listen 为空。")
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
	color.Green("WebUI: http://%s", cfg.WebUI.Listen)
	return true
}

func (m *Manager) ApplyConfig(cfg *config.Config) {
	if cfg == nil {
		cfg = config.Get()
	}
	m.watchCtrl.UpdateOptions(watch.DefaultOptions(cfg))

	if cfg.Modules.Bot {
		m.StartBot()
	} else {
		go m.StopBot()
	}
	if cfg.Modules.Watch {
		go m.StartWatch(context.Background())
	} else {
		go m.StopWatch()
	}
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
			Status:      "运行中",
		},
		m.botState(cfg),
		m.watchState(cfg),
	}
}

func (m *Manager) SetModuleEnabled(ctx context.Context, id string, enabled bool) (webui.ModuleState, error) {
	next, err := cloneConfig(config.Get())
	if err != nil {
		return webui.ModuleState{}, err
	}

	id = strings.ToLower(strings.TrimSpace(id))
	switch id {
	case "bot":
		next.Modules.Bot = enabled
	case "watch":
		next.Modules.Watch = enabled
	case "webui":
		return webui.ModuleState{}, errors.New("webui cannot be disabled from the web panel")
	default:
		return webui.ModuleState{}, fmt.Errorf("unknown module %q", id)
	}

	if err := config.Set(next); err != nil {
		return webui.ModuleState{}, err
	}

	switch id {
	case "bot":
		if enabled {
			m.StartBot()
		} else {
			m.StopBot()
		}
		return m.botState(next), nil
	case "watch":
		if enabled {
			_ = m.StartWatch(ctx)
		} else {
			m.StopWatch()
		}
		return m.watchState(next), nil
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
			Proxy:                 cfg.Proxy,
			Namespace:             cfg.Namespace,
			NTP:                   cfg.NTP,
			ReconnectTimeout:      time.Duration(cfg.ReconnectTimeout) * time.Second,
			Watch:                 watch.DefaultOptions(cfg),
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
	if cfg == nil || !cfg.Modules.Watch {
		return nil
	}
	if m.watchCtrl.Running() {
		return nil
	}
	if err := m.checkSession(ctx); err != nil {
		return err
	}
	m.watchCtrl.UpdateOptions(watch.DefaultOptions(cfg))
	m.watchCtrl.Start()
	return nil
}

func (m *Manager) StopWatch() {
	m.watchCtrl.Stop()
}

func (m *Manager) Shutdown() {
	m.StopBot()
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
	if config.Get().Modules.Watch {
		go func() {
			if err := m.StartWatch(context.Background()); err != nil {
				m.Notify(context.Background(), "登录成功，但监听下载未启动："+err.Error())
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
		status = "未启动"
	}
	if cfg != nil && cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) == "" {
		status = "已启用，等待填写 Bot Token。"
	}
	if err != nil {
		status = err.Error()
	}
	return webui.ModuleState{
		ID:          "bot",
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
	status := "未启动"
	if running {
		status = "运行中"
	} else if err := m.watchCtrl.LastError(); err != nil {
		status = "已停止：" + err.Error()
	} else if cfg != nil && cfg.Modules.Watch {
		status = "已启用，等待 Telegram 用户登录或启动。"
	}
	return webui.ModuleState{
		ID:          "watch",
		Name:        "监听下载",
		Description: "监听 Telegram 表情触发，提供本地下载链接，并把任务提交到 aria2。",
		Enabled:     cfg != nil && cfg.Modules.Watch,
		Running:     running,
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
		Proxy:            cfg.Proxy,
		NTP:              cfg.NTP,
		ReconnectTimeout: time.Duration(cfg.ReconnectTimeout) * time.Second,
	})
	return err
}

func (m *Manager) hasRunnableModule(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return cfg.Modules.Bot && strings.TrimSpace(cfg.Bot.Token) != ""
}

func (m *Manager) setBotStopped(status string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.botCancel = nil
	m.botDone = nil
	m.botStatus = status
	m.botErr = err
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

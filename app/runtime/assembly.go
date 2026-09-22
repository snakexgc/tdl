package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fatih/color"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/application"
	timesync "github.com/snakexgc/tdl/application/time.sync"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

func revision(values ...any) string {
	data, err := json.Marshal(values)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func stopResource(name string, stop func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		bounded, cancel := context.WithTimeout(ctx, moduleStopTimeout)
		defer cancel()
		started := time.Now()
		logger := logctx.From(ctx).With(zap.String("resource", name))
		logger.Debug("正在停止运行模块")
		done, reported := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(reported)
			timer := time.NewTimer(time.Second)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
				color.Yellow("⏳ 正在等待%s停止…", name)
				logger.Info("正在等待运行模块停止")
			}
		}()
		defer func() { close(done); <-reported }()
		err := stop(bounded)
		if err != nil {
			logger.Error("运行模块停止失败", zap.Duration("elapsed", time.Since(started)), zap.Error(err))
		} else {
			logger.Debug("运行模块已停止", zap.Duration("elapsed", time.Since(started)))
		}
		return err
	}
}

func (m *Manager) componentEnabled(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configured[id]
}

func (m *Manager) connectionNeeded(cfg *config.Config) bool {
	return cfg != nil && (cfg.Modules.Watch || cfg.Modules.Forward || m.componentEnabled("downloader.local") || m.componentEnabled("forwarder"))
}

// managedUnits is the production composition boundary. Generic lifecycle code
// knows neither module configuration. Backend factories stay here.
func (m *Manager) managedUnits(cfg *config.Config) []rte.ManagedUnit {
	return append(m.foundationUnits(cfg), []rte.ManagedUnit{
		{
			ID: timesync.ID, Enabled: m.componentEnabled(timesync.ID),
			Running: func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.timeHost != nil },
			Start: func(context.Context) error {
				host, err := application.TimeHost(m.parent, m.downloadAccount, m.componentStore, m.timeProbe)
				if err != nil {
					return err
				}
				value, err := host.Resolve(ports.ClockName)
				if err != nil {
					_ = host.Stop(context.Background())
					return err
				}
				m.clock.Bind(value.(ports.Clock))
				m.mu.Lock()
				m.timeHost = host
				m.mu.Unlock()
				return nil
			},
			Stop: stopResource("时间同步", func(ctx context.Context) error {
				m.mu.Lock()
				host := m.timeHost
				m.mu.Unlock()
				if host == nil {
					return nil
				}
				if err := host.Stop(ctx); err != nil {
					return err
				}
				m.clock.Bind(nil)
				m.mu.Lock()
				m.timeHost = nil
				m.mu.Unlock()
				return nil
			}),
		},
		{
			ID: moduleIDHTTP, Enabled: true, Revision: revision(config.HTTPListenAddr(cfg)), Running: m.httpCtrl.Running,
			Update: func(context.Context) error { m.httpService.UpdateConfig(cfg); return nil },
			Start: func(context.Context) error {
				if !m.httpCtrl.Start() && !m.httpCtrl.Running() {
					return m.httpCtrl.LastError()
				}
				return nil
			}, Stop: stopResource("HTTP 下载服务", m.httpCtrl.StopContext),
		},
		{
			ID: moduleIDAria2, Enabled: config.Aria2Enabled(cfg), Revision: revision(effectiveAria2ManagerConfig(cfg)), Running: m.aria2Process.Running,
			Update: func(context.Context) error {
				next := effectiveAria2ManagerConfig(cfg)
				m.mu.Lock()
				defer m.mu.Unlock()
				if m.aria2Config != next {
					manager := aria2.NewManager(cfg, m.namespaceKV, logctx.From(m.parent), m.kvEngine)
					manager.SetComponentStore(m.componentStore)
					m.aria2Mgr = manager
					m.aria2Config = next
				}
				m.aria2Mgr.UpdateTransferLimits(cfg.Limit, cfg.PoolSize)
				m.aria2Mgr.UpdateLinks(cfg.HTTP)
				return nil
			}, Start: func(context.Context) error {
				m.StartAria2Manager()
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.aria2Err
			}, Stop: stopResource("aria2 管理服务", m.aria2Process.Stop),
		},
		{
			ID: moduleIDBot, Enabled: cfg.Modules.Bot, Requires: []string{accountResource}, Revision: revision(cfg.Bot.Token, m.botProxy(cfg)), Running: m.botProcess.Running,
			Update: func(ctx context.Context) error {
				m.mu.Lock()
				refresh := m.botRefresh
				m.mu.Unlock()
				if refresh != nil {
					return refresh(ctx)
				}
				return nil
			},
			Start: func(context.Context) error {
				m.StartBot()
				if !m.botProcess.Running() {
					return fmt.Errorf("bot did not start: configure a valid token")
				}
				return nil
			},
			Stop: stopResource("Telegram 机器人", func(ctx context.Context) error {
				if err := m.botProcess.Stop(ctx); err != nil {
					return err
				}
				m.setNotifier(nil)
				return nil
			}),
		},
		{
			ID: "panel", Enabled: cfg.Modules.WebUI && m.componentEnabled(panelComponentID), Requires: []string{accountResource}, Revision: revision(cfg.WebUI), Running: m.panelProcess.Running,
			Start: func(context.Context) error {
				if !m.StartWebUI(m.parent) {
					return fmt.Errorf("panel did not start: verify address and credentials")
				}
				return nil
			}, Stop: stopResource("Web 管理面板", m.panelProcess.Stop),
		},
		{
			ID: moduleIDWatch, Enabled: m.connectionNeeded(cfg), Requires: []string{accountResource},
			Revision: revision(config.EffectiveProxy(cfg), cfg.Delay, cfg.ReconnectTimeout), Running: m.watchCtrl.Running,
			Update: func(context.Context) error { m.watchCtrl.UpdateOptions(m.watchOptions(cfg)); return nil },
			Start:  m.StartWatch, Stop: stopResource("Telegram 监听", m.watchCtrl.StopContext),
		},
	}...)
}

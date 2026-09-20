package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/app/aria2"
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

func stopResource(stop func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		bounded, cancel := context.WithTimeout(ctx, moduleStopTimeout)
		defer cancel()
		return stop(bounded)
	}
}

func (m *Manager) componentEnabled(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.configured == nil || m.configured[id]
}

func (m *Manager) connectionNeeded(cfg *config.Config) bool {
	return cfg != nil && (cfg.Modules.Watch || cfg.Modules.Forward || m.componentEnabled("downloader.local") || m.componentEnabled("forwarder"))
}

// managedUnits is the production composition boundary. Generic lifecycle code
// knows neither module names nor legacy fields. Backend factories stay here.
func (m *Manager) managedUnits(cfg *config.Config) []rte.ManagedUnit {
	return append(m.foundationUnits(cfg), []rte.ManagedUnit{
		{
			ID: "http", Enabled: cfg.Modules.HTTP, Requires: []string{accountResource}, Revision: revision(config.HTTPListenAddr(cfg)), Running: m.httpCtrl.Running,
			Update: func(context.Context) error { m.httpService.UpdateConfig(cfg); return nil },
			Start: func(context.Context) error {
				if !m.httpCtrl.Start() && !m.httpCtrl.Running() {
					return m.httpCtrl.LastError()
				}
				return nil
			}, Stop: stopResource(m.httpCtrl.StopContext),
		},
		{
			ID: moduleIDAria2, Enabled: cfg.Modules.Aria2, Revision: revision(effectiveAria2ManagerConfig(cfg)), Running: m.aria2Process.Running,
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
				m.aria2Mgr.UpdateTransferLimits(config.EffectiveLimit(cfg), config.EffectivePoolSize(cfg))
				m.aria2Mgr.UpdateLinks(cfg.HTTP)
				return nil
			}, Start: func(context.Context) error {
				m.StartAria2Manager()
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.aria2Err
			}, Stop: stopResource(m.aria2Process.Stop),
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
			Stop: stopResource(func(ctx context.Context) error {
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
			}, Stop: stopResource(m.panelProcess.Stop),
		},
		{
			ID: moduleIDWatch, Enabled: m.connectionNeeded(cfg), Requires: []string{accountResource},
			Revision: revision(config.EffectiveProxy(cfg), cfg.NTP, cfg.Delay, cfg.ReconnectTimeout), Running: m.watchCtrl.Running,
			Update: func(context.Context) error { m.watchCtrl.UpdateOptions(m.watchOptions(cfg)); return nil },
			Start: func(ctx context.Context) error {
				bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return m.StartWatch(bounded)
			}, Stop: stopResource(m.watchCtrl.StopContext),
		},
	}...)
}

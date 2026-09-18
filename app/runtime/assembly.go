package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/app/aria2"
	"github.com/snakexgc/tdl/application"
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

// managedUnits is the production composition boundary. Generic lifecycle code
// knows neither module names nor legacy fields. Backend factories stay here.
func (m *Manager) managedUnits(cfg *config.Config) []rte.ManagedUnit {
	watchDependencies := []string{accountResource, watchPolicyResource}
	if cfg.Modules.Watch {
		watchDependencies = append(watchDependencies, downloadResource)
	}
	if cfg.Modules.Watch && config.EffectiveDownloaderMode(cfg) != config.DownloaderModeLocal {
		watchDependencies = append(watchDependencies, "http")
		if watchAutoDownloadEnabled(cfg) {
			watchDependencies = append(watchDependencies, moduleIDAria2)
		}
	}
	return append(m.foundationUnits(cfg), []rte.ManagedUnit{
		{
			ID: "http", Enabled: cfg.Modules.HTTP, Requires: []string{accountResource}, Revision: revision(cfg.HTTP), Running: m.httpCtrl.Running,
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
				return nil
			}, Start: func(context.Context) error {
				m.StartAria2Manager()
				m.mu.Lock()
				defer m.mu.Unlock()
				return m.aria2Err
			}, Stop: stopResource(m.aria2Process.Stop),
		},
		{
			ID: "bot", Enabled: cfg.Modules.Bot, Requires: []string{accountResource}, Revision: revision(cfg.Bot.Token, m.botProxy(cfg), m.commandStates()), Running: m.botProcess.Running,
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
				m.setBotStopped("stopped", nil)
				return nil
			}),
		},
		{
			ID: "panel", Enabled: cfg.Modules.WebUI && m.componentEnabled("panel.webui"), Requires: []string{accountResource}, Revision: revision(cfg.WebUI), Running: m.panelProcess.Running,
			Start: func(context.Context) error {
				if !m.StartWebUI(m.parent) {
					return fmt.Errorf("panel did not start: verify address and credentials")
				}
				return nil
			}, Stop: stopResource(m.panelProcess.Stop),
		},
		{
			ID: "watch", Enabled: cfg.Modules.Watch || cfg.Modules.Forward, Requires: watchDependencies,
			Revision: revision(cfg.Modules.Watch, cfg.Modules.Forward, cfg.Downloader, cfg.Forward, watchAutoDownloadEnabled(cfg), cfg.Aria2.Dir, config.EffectiveProxy(cfg), cfg.NTP, cfg.Delay, cfg.ReconnectTimeout, m.connectionStates()), Running: m.watchCtrl.Running,
			Update: func(context.Context) error { m.watchCtrl.UpdateOptions(m.watchOptions(cfg)); return nil },
			Start: func(ctx context.Context) error {
				bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()
				return m.StartWatch(bounded)
			}, Stop: stopResource(m.watchCtrl.StopContext),
		},
	}...)
}

func (m *Manager) connectionStates() map[string]bool {
	catalog, err := application.Catalog()
	if err != nil {
		panic(err)
	}
	states := map[string]bool{}
	for _, definition := range catalog.Definitions() {
		if definition.Scope == rte.ConnectionScope {
			states[definition.Manifest.ID] = m.componentEnabled(definition.Manifest.ID)
		}
	}
	return states
}

func (m *Manager) commandStates() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	states := map[string]bool{}
	for id, enabled := range m.configured {
		states[id] = enabled
	}
	return states
}

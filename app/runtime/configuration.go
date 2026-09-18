package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/internal/componentconfig"
	"github.com/snakexgc/tdl/pkg/config"
)

func (m *Manager) SetComponentEnabled(ctx context.Context, id string, enabled bool, revision string) error {
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if m.directory == nil || m.configSource == nil {
		return fmt.Errorf("component assembly is unavailable")
	}
	if err := m.directory.SetEnabledWithRevision(ctx, id, enabled, revision); err != nil {
		return err
	}
	cfg, flags, err := componentconfig.Load(ctx, m.componentStore, config.From(m.parent))
	if err != nil {
		return err
	}
	m.configSource.Replace(cfg)
	m.mu.Lock()
	m.configured = flags
	m.mu.Unlock()
	return m.applyConfigLocked(cfg, m.applyVersion.Add(1), true)
}

func (m *Manager) botProxy(cfg *config.Config) string {
	if m.componentStore != nil || cfg.Bot.Proxy != "" {
		return cfg.Bot.Proxy
	}
	return config.EffectiveProxy(cfg)
}

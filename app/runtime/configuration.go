package runtime

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func (m *Manager) SetComponentEnabled(ctx context.Context, id string, enabled bool, revision string) error {
	if id == rangeComponentID && !enabled {
		return fmt.Errorf("HTTP 下载服务必须随程序启动，不能禁用")
	}
	m.applyMu.Lock()
	defer m.applyMu.Unlock()
	if m.directory == nil || m.configSource == nil {
		return fmt.Errorf("component assembly is unavailable")
	}
	return m.directory.SetEnabledWithRevision(ctx, id, enabled, revision)
}

func startupStore(ctx context.Context, saved *rteconfig.Store) (*rteconfig.Store, error) {
	catalog, err := application.Catalog()
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, definition := range catalog.Definitions() {
		ids = append(ids, definition.Manifest.ID)
	}
	return saved.Snapshot(ctx, ids)
}

func (m *Manager) systemRepository() (ports.ConfigurationManager, error) {
	if m.configurationHost == nil {
		return nil, fmt.Errorf("system configuration repository is unavailable")
	}
	value, err := m.configurationHost.Resolve(ports.ConfigurationManagerName)
	if err != nil {
		return nil, err
	}
	return value.(ports.ConfigurationManager), nil
}

func (m *Manager) System(ctx context.Context) (ports.SystemConfiguration, error) {
	repository, err := m.systemRepository()
	if err != nil {
		return ports.SystemConfiguration{}, err
	}
	return repository.System(ctx)
}

func (m *Manager) SetSystem(ctx context.Context, before, next ports.SystemConfiguration) error {
	repository, err := m.systemRepository()
	if err != nil {
		return err
	}
	return repository.SetSystem(ctx, before, next)
}

func (m *Manager) botProxy(cfg *config.Config) string {
	return config.EffectiveProxy(cfg)
}

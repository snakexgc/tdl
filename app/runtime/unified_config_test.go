package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	configurationmanager "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/configuration"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

func TestUnifiedConfigSavesHotSettingsWithoutRestartingAccount(t *testing.T) {
	ctx, home := context.Background(), t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	service, err := configuration.Open(ctx, home)
	require.NoError(t, err)
	for _, id := range []string{consoleComponentID, downloadTriggerComponentID, forwardTriggerComponentID, aria2ComponentID, rangeComponentID, panelComponentID} {
		saveComponent(t, service.Store(), id, false, nil)
	}
	host, err := application.ConfigurationHost(ctx, types.DefaultAccount, service)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	m := NewManager(config.WithSource(ctx, config.NewSource(cfg)), nil, nil, Options{ComponentStore: service.Store(), ConfigurationHost: host})
	t.Cleanup(m.Shutdown)
	require.NoError(t, m.configurationErr)
	owner := m.accountHost
	require.NoError(t, m.SaveComponentConfiguration(ctx, "account.telegram", map[string]any{"file_limit": 3, "dc_pool_size": 16}))
	m.transitionWG.Wait()
	require.Equal(t, 3, config.From(m.parent).Limit)
	require.Equal(t, 16, config.From(m.parent).PoolSize)
	require.Same(t, owner, m.accountHost)
	require.NoError(t, m.SetComponentEnabled(ctx, aria2ComponentID, false, ""))
	m.transitionWG.Wait()
	data, err := os.ReadFile(filepath.Join(home, configurationmanager.Filename))
	require.NoError(t, err)
	var doc configurationmanager.Document
	require.NoError(t, json.Unmarshal(data, &doc))
	require.EqualValues(t, 3, doc.Components["account.telegram"].Values["file_limit"])
	require.False(t, doc.Components[aria2ComponentID].Enabled)
	entries, enabled := m.ComponentConfigurations()
	require.True(t, enabled)
	found := false
	for _, entry := range entries {
		if entry.ID == configurationmanager.ID {
			found = true
			require.Equal(t, rte.Running, entry.State)
		}
	}
	require.True(t, found)
	require.NoDirExists(t, filepath.Join(home, "components"))
}

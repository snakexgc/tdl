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

func TestUnifiedConfigStagesSettingsUntilRestart(t *testing.T) {
	const accountID, limitField = "account.telegram", "file_limit"
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
	require.NoError(t, m.SaveComponentConfiguration(ctx, accountID, map[string]any{limitField: 3, "dc_pool_size": 16}))
	require.NoError(t, m.SaveComponentConfiguration(ctx, panelComponentID, map[string]any{"password": "pending-password"}))
	require.NoError(t, m.SetComponentEnabled(ctx, "filter.rules", false, ""))
	before, err := m.System(ctx)
	require.NoError(t, err)
	next := before
	next.Debug = true
	require.NoError(t, m.SetSystem(ctx, before, next))
	m.transitionWG.Wait()
	require.Equal(t, 1, config.From(m.parent).Limit)
	require.Equal(t, 8, config.From(m.parent).PoolSize)
	require.Same(t, owner, m.accountHost)
	require.False(t, config.From(m.parent).Debug)
	// A connection rebuild must still read the startup settings.
	active, err := m.componentStore.Load(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, json.Number("1"), active.Values[limitField])
	require.Error(t, m.SaveComponentConfiguration(ctx, accountID, map[string]any{limitField: -1}))
	require.NoError(t, m.SetComponentEnabled(ctx, aria2ComponentID, false, ""))
	m.transitionWG.Wait()
	data, err := os.ReadFile(filepath.Join(home, configurationmanager.Filename))
	require.NoError(t, err)
	var doc configurationmanager.Document
	require.NoError(t, json.Unmarshal(data, &doc))
	require.EqualValues(t, 3, doc.Components[accountID].Values[limitField])
	require.False(t, doc.Components[aria2ComponentID].Enabled)
	require.True(t, doc.System.Debug)
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
	for _, entry := range entries {
		switch entry.ID {
		case accountID:
			require.True(t, entry.PendingRestart)
			require.Len(t, entry.Changes, 2)
		case panelComponentID:
			require.True(t, entry.PendingRestart, "disabled components also track pending edits")
			require.Len(t, entry.Changes, 1)
			require.True(t, entry.Changes[0].Secret)
			public, marshalErr := json.Marshal(entry)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(public), "pending-password")
		case "filter.rules":
			require.True(t, entry.PendingRestart)
			require.NotNil(t, entry.ActiveEnabled)
			require.True(t, *entry.ActiveEnabled)
			require.False(t, entry.Enabled)
		}
	}
	// Reverting to the startup values removes the pending change without restarting.
	require.NoError(t, m.SaveComponentConfiguration(ctx, accountID, map[string]any{limitField: 1, "dc_pool_size": 8}))
	entries, _ = m.ComponentConfigurations()
	for _, entry := range entries {
		if entry.ID == accountID {
			require.False(t, entry.PendingRestart)
			require.Empty(t, entry.Changes)
		}
	}
	require.NoError(t, m.SaveComponentConfiguration(ctx, accountID, map[string]any{limitField: 3}))
	// Opening the same file again does not lose the pending differences.
	reopened, err := configuration.Open(ctx, home)
	require.NoError(t, err)
	catalog, err := application.Catalog()
	require.NoError(t, err)
	refreshed := rte.NewDirectory(catalog, reopened.Store()).WithActiveStore(m.componentStore)
	for _, entry := range refreshed.Configurations(ctx) {
		if entry.ID == accountID {
			require.Len(t, entry.Changes, 1)
			require.EqualValues(t, 1, entry.Changes[0].Before)
			require.EqualValues(t, 3, entry.Changes[0].After)
		}
	}
	m.Shutdown()
	startup := config.DefaultConfig()
	startup.Debug = doc.System.Debug
	restarted := NewManager(config.WithSource(ctx, config.NewSource(startup)), nil, nil, Options{ComponentStore: reopened.Store(), ConfigurationHost: host})
	t.Cleanup(restarted.Shutdown)
	require.NoError(t, restarted.configurationErr)
	require.Equal(t, 3, config.From(restarted.parent).Limit)
	require.True(t, config.From(restarted.parent).Debug)
	require.Equal(t, "pending-password", config.From(restarted.parent).WebUI.Password)
	entries, _ = restarted.ComponentConfigurations()
	for _, entry := range entries {
		require.False(t, entry.PendingRestart, entry.ID)
		require.Empty(t, entry.Changes, entry.ID)
		if entry.ID == "filter.rules" {
			require.False(t, *entry.ActiveEnabled)
		}
	}
	require.NoDirExists(t, filepath.Join(home, "components"))
}

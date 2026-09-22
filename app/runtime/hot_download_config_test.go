package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

func TestCredentialSaveKeepsActiveAccountResources(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	store := newStoppedComponentStore(t)
	manager := NewManager(config.WithSource(ctx, config.NewSource(cfg)), nil, nil, Options{ComponentStore: store})
	t.Cleanup(manager.Shutdown)
	require.NoError(t, manager.configurationErr)
	owner, connections := manager.accountHost, manager.connections
	require.NoError(t, manager.SaveComponentConfiguration(ctx, "account.telegram", map[string]any{apiIDField: 12345, apiHashField: "0123456789abcdef0123456789abcdef", fieldUseBuiltin: false}))
	manager.transitionWG.Wait()
	require.Same(t, owner, manager.accountHost, "credential edits wait for process restart")
	require.Same(t, connections, manager.connections)
	credentials, err := manager.Resolve(ctx, manager.downloadAccount)
	require.NoError(t, err)
	require.NotEqual(t, 12345, credentials.App.AppID)
}

func TestComponentEditsKeepAccountBootConfiguration(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.Namespace = "isolated"
	cfg.Delay = 42
	cfg.Modules = config.ModulesConfig{}
	store := newStoppedComponentStore(t)
	saveComponent(t, store, "account.telegram", true, map[string]any{"delay_seconds": cfg.Delay})
	m := NewManager(config.WithSource(ctx, config.NewSource(cfg)), nil, nil, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	require.NoError(t, m.configurationErr)
	owner := m.accountHost
	require.NoError(t, m.SaveComponentConfiguration(ctx, ports.NamingRulesName, map[string]any{fieldDirectory: "saved/P"}))
	m.transitionWG.Wait()
	require.NoError(t, m.SetComponentEnabled(ctx, aria2ComponentID, false, ""))
	m.transitionWG.Wait()
	current := config.From(m.parent)
	require.Equal(t, cfg.Namespace, current.Namespace)
	require.Equal(t, cfg.Delay, current.Delay)
	require.Same(t, owner, m.accountHost)
	require.Equal(t, m.downloadAccount, m.policies.Health().Account)
}

func TestConnectionFeatureTogglesDoNotChangeTransportDependencies(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{Watch: true, Forward: true}
	m := NewManager(config.WithSource(context.Background(), config.NewSource(cfg)), nil, nil, Options{ComponentStore: newStoppedComponentStore(t)})
	t.Cleanup(m.Shutdown)
	watchUnit := func(cfg *config.Config) rte.ManagedUnit {
		for _, unit := range m.managedUnits(cfg) {
			if unit.ID == moduleIDWatch {
				return unit
			}
		}
		t.Fatal("missing connection owner")
		return rte.ManagedUnit{}
	}
	original := watchUnit(cfg)
	nextForward := *cfg
	nextForward.Forward.Target = "chat:123"
	nextForward.Forward.Listen = []string{"channel:456"}
	nextForward.Forward.Silent = true
	require.Equal(t, original.Revision, watchUnit(&nextForward).Revision)
	for _, mode := range []config.ModulesConfig{{Watch: true}, {Forward: true}} {
		next := *cfg
		next.Modules = mode
		m.configured = map[string]bool{ports.FilterRulesName: false, ports.NamingRulesName: false, forwardComponentID: false, localComponentID: false}
		current := watchUnit(&next)
		require.True(t, current.Enabled)
		require.Equal(t, original.Revision, current.Revision)
		require.Equal(t, original.Requires, current.Requires)
	}
	for _, consumer := range []string{localComponentID, forwardComponentID} {
		next := *cfg
		next.Modules = config.ModulesConfig{}
		m.configured = map[string]bool{consumer: true}
		current := watchUnit(&next)
		require.True(t, current.Enabled, "enabled queue consumer must retain the connection")
		require.Equal(t, original.Revision, current.Revision)
		m.configured = map[string]bool{}
		require.False(t, watchUnit(&next).Enabled)
	}
}

func TestDownloadMetadataDoesNotRestartTransports(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	ctx := config.WithSource(context.Background(), config.NewSource(cfg))
	manager := NewManager(ctx, nil, nil, Options{ComponentStore: newStoppedComponentStore(t)})
	t.Cleanup(manager.Shutdown)
	find := func(units []rte.ManagedUnit, id string) rte.ManagedUnit {
		for _, unit := range units {
			if unit.ID == id {
				return unit
			}
		}
		t.Fatalf("missing resource %s", id)
		return rte.ManagedUnit{}
	}
	before, original := manager.managedUnits(cfg), manager.aria2Mgr
	next := *cfg
	next.Aria2.Dir = "/another/remote/directory"
	next.HTTP.PublicBaseURL = "https://new.example"
	next.HTTP.DownloadLinkTTLHours = 72
	next.Downloader.Executors = []string{config.DownloadExecutorLocal}
	after := manager.managedUnits(&next)
	for _, id := range []string{moduleIDHTTP, moduleIDAria2, moduleIDWatch, downloadResource} {
		require.Equal(t, find(before, id).Revision, find(after, id).Revision, id)
	}
	require.NoError(t, find(after, moduleIDAria2).Update(ctx))
	require.Same(t, original, manager.aria2Mgr)
	next.Aria2.RPCURL = "http://127.0.0.1:16800/jsonrpc"
	require.NotEqual(t, find(after, moduleIDAria2).Revision, find(manager.managedUnits(&next), moduleIDAria2).Revision)
}

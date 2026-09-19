package runtime

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/componentconfig"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func TestProductionStopTimeoutRetainsAccountOwnerUntilConsumerExits(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	ctx := config.WithSource(context.Background(), config.NewSource(cfg))
	engine, err := kv.New(kv.DriverFile, map[string]any{"path": filepath.Join(t.TempDir(), "state")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open(cfg.Namespace)
	require.NoError(t, err)
	manager := NewManager(ctx, engine, store, Options{})
	require.NoError(t, manager.configurationErr)
	require.NotNil(t, manager.accountHost)
	require.NotNil(t, manager.downloadHost)
	owner := manager.connections
	release, entered := make(chan struct{}), make(chan struct{})
	_, err = manager.botProcess.Start(func(context.Context) error {
		close(entered)
		<-release
		return nil
	}, rte.Recovery{})
	require.NoError(t, err)
	<-entered
	t.Cleanup(func() { manager.Shutdown() })
	t.Cleanup(func() { close(release) })
	units := manager.managedUnits(cfg)
	for i := range units {
		units[i].Enabled = false
	}
	bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	require.Error(t, manager.reconciler.Reconcile(bounded, units))
	require.Same(t, owner, manager.connections)
	require.NotNil(t, manager.accountHost)
	require.NoError(t, owner.Context().Err(), "consumer still owns the transport")
	// Cleanup releases the deliberately blocked consumer before retrying shutdown.
}

func TestProductionFoundationsAreClosedByTheDeclaredGraph(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	ctx := config.WithSource(context.Background(), config.NewSource(cfg))
	manager := NewManager(ctx, nil, nil, Options{})
	require.NoError(t, manager.configurationErr)
	owner := manager.connections
	manager.Shutdown()
	require.Nil(t, manager.accountHost)
	require.Nil(t, manager.policies)
	require.Nil(t, manager.downloadHost)
	require.ErrorIs(t, owner.Context().Err(), context.Canceled)
	manager.Shutdown()
}

func TestProductionComponentToggleRetainsIndependentInstances(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	catalog, err := application.Catalog()
	require.NoError(t, err)
	documents, err := componentconfig.Export(cfg, catalog)
	require.NoError(t, err)
	path := t.TempDir()
	store := rteconfig.NewStore(path)
	for id, document := range documents {
		view, err := catalog.View(ctx, id, document.Values)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, id, document.Enabled, view))
	}
	manager := NewManager(config.WithSource(ctx, config.NewSource(cfg)), nil, nil, Options{ComponentConfigDir: path})
	t.Cleanup(manager.Shutdown)
	require.NoError(t, manager.configurationErr)
	accountOwner, policyOwner := manager.accountHost, manager.policies
	credentials, err := manager.componentPort(ports.TelegramCredentialsName)
	require.NoError(t, err)
	opts := manager.watchOptions(cfg)
	version, err := store.Revision(ctx, "filter.rules")
	require.NoError(t, err)
	require.NoError(t, manager.SetComponentEnabled(ctx, "filter.rules", false, version))
	manager.transitionWG.Wait()
	_, err = manager.componentPort(ports.FilterRulesName)
	require.Error(t, err)
	_, err = manager.componentPort(ports.TelegramCredentialsName)
	require.NoError(t, err)
	require.ErrorIs(t, manager.SetComponentEnabled(ctx, "filter.rules", true, version), rte.ErrConfigurationConflict)
	require.NoError(t, manager.SetComponentEnabled(ctx, "filter.rules", true, ""))
	manager.transitionWG.Wait()
	_, err = manager.componentPort(ports.FilterRulesName)
	require.NoError(t, err)
	require.Same(t, accountOwner, manager.accountHost)
	require.Same(t, policyOwner, manager.policies)
	current, err := manager.componentPort(ports.TelegramCredentialsName)
	require.NoError(t, err)
	require.Same(t, credentials, current)
	in := ports.ReactionInput{Account: manager.downloadAccount, Reactions: []ports.Reaction{{Mine: true, Value: "x"}}}
	require.True(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, manager.SetComponentEnabled(ctx, "trigger.reaction", false, ""))
	manager.transitionWG.Wait()
	require.False(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, manager.SetComponentEnabled(ctx, "trigger.reaction", true, ""))
	manager.transitionWG.Wait()
	require.True(t, opts.Reaction.Matches(ctx, in))
	require.Same(t, accountOwner, manager.accountHost)
}

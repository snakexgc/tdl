package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestProductionPolicyHostPreservesLastValidConfiguration(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, newComponentStore(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{policies: host, filter: filter, naming: naming}
	require.NoError(t, manager.initDirectory())
	opts := manager.watchOptions(cfg)
	require.NotNil(t, opts.Filter)
	require.NotNil(t, opts.Naming)
	input := ports.NamingInput{BaseDir: "/downloads", Data: ports.NamingData{FileName: "video.mp4"}}
	before, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Error(t, host.ReconfigureBatch(ctx, map[string]map[string]any{"naming.rules": {"filename": "{{"}}))
	after, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.NoError(t, host.ReconfigureBatch(ctx, map[string]map[string]any{"naming.rules": {"filename": "new-F"}}))
	after, err = opts.Naming.Render(ctx, input)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestProductionStoredPoliciesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	store := newComponentStore(t)
	saveComponent(t, store, "naming.rules", true, map[string]any{fieldFilename: "configured-F"})
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{policies: host, filter: filter, naming: naming, componentStore: store, savedStore: store}
	require.NoError(t, manager.initDirectory())
	input := ports.NamingInput{BaseDir: "/downloads", Data: ports.NamingData{FileName: "sample.mp4"}}
	before, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Contains(t, before.FileName, "configured-")
	require.NoError(t, manager.SaveComponentConfiguration(ctx, "naming.rules", map[string]any{fieldFilename: "saved-F"}))
	after, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Equal(t, before, after)
	require.Error(t, manager.SaveComponentConfiguration(ctx, "naming.rules", map[string]any{fieldFilename: "{{"}))
	require.NoError(t, host.Stop(ctx))
	restarted, _, reloaded, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Stop(ctx)) })
	result, err := reloaded.Render(ctx, input)
	require.NoError(t, err)
	require.Contains(t, result.FileName, "saved-")
	require.NotEqual(t, before, result)
}

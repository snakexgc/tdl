package downloadcontrol

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TestRoutingConfigValidatesAndPersistsIndependentLocalRoot(t *testing.T) {
	ctx := context.Background()
	store := config.NewStore(t.TempDir())
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, nil))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	value, err := host.Resolve(ports.DownloadRoutingName)
	require.NoError(t, err)
	routing := value.(ports.DownloadRouting)
	for _, values := range []map[string]any{
		{executorsField: []string{localExecutor}},
		{executorsField: []string{"other"}},
		{executorsField: []string{aria2Executor, aria2Executor}},
		{executorsField: []string{httpExecutor, aria2Executor}},
	} {
		require.Error(t, host.PatchSaved(ctx, ID, values, store))
	}
	root := t.TempDir()
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{executorsField: []string{aria2Executor, localExecutor, httpExecutor}, "local_root": root}, store))
	route, err := routing.Route(ctx, types.DefaultAccount)
	require.NoError(t, err)
	require.Equal(t, []string{aria2Executor, localExecutor, httpExecutor}, route.Executors)
	require.Equal(t, root, route.LocalRoot)
	route.Executors[0] = "mutated"
	document, err := store.Load(ctx, ID)
	require.NoError(t, err)
	require.NoError(t, host.Stop(ctx))
	restarted, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: document.Values})
	require.NoError(t, err)
	require.Equal(t, rte.Running, restarted.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, restarted.Stop(ctx)) })
	value, err = restarted.Resolve(ports.DownloadRoutingName)
	require.NoError(t, err)
	route, err = value.(ports.DownloadRouting).Route(ctx, types.DefaultAccount)
	require.NoError(t, err)
	require.Equal(t, aria2Executor, route.Executors[0])
	_, err = value.(ports.DownloadRouting).Route(ctx, "wrong-account")
	require.Error(t, err)
}

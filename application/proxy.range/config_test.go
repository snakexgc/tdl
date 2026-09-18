package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TestRangeConfigurationPersistsAndAppliesToHandler(t *testing.T) {
	ctx := context.Background()
	store := config.NewStore(t.TempDir())
	build := func(values map[string]any) (*rte.Runtime, *Handler) {
		handler := New(testSource{}, nil, 0)
		registry := rte.NewRegistry()
		require.NoError(t, Register(registry, handler))
		host, err := registry.Build("account", nil, map[string]map[string]any{ID: values})
		require.NoError(t, err)
		require.Equal(t, rte.Running, host.Start(ctx)[0].State)
		t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
		return host, handler
	}
	host, handler := build(nil)
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{clientWaitField: 2, "task_cleanup_seconds": 15}, store))
	require.Equal(t, 2*time.Second, handler.policy().wait)
	require.Equal(t, 15*time.Second, handler.policy().tasks)
	require.Error(t, host.PatchSaved(ctx, ID, map[string]any{clientWaitField: 0}, store))
	require.NoError(t, host.Stop(ctx))
	document, err := store.Load(ctx, ID)
	require.NoError(t, err)
	_, next := build(document.Values)
	require.Equal(t, handler.policy(), next.policy())
}

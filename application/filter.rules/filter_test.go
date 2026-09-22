package filterrules

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestAtomicReconfigureAndRejectedRange(t *testing.T) {
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	run, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {includeField: []string{"mp4"}, minMBField: 1}})
	require.NoError(t, err)
	ctx := context.Background()
	require.Equal(t, rte.Running, run.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, run.Stop(ctx)) })
	value, err := run.Resolve(ports.FilterRulesName)
	require.NoError(t, err)
	filter := value.(ports.FilterRules)
	ok, reason := filter.ShouldHandle(ctx, ports.FilterInput{Name: "clip.mkv", Size: 0})
	require.False(t, ok)
	require.Equal(t, ports.ExtensionExcluded, reason)
	require.Error(t, run.Reconfigure(ctx, ID, map[string]any{minMBField: 5, maxMBField: 2}))
	ok, reason = filter.ShouldHandle(ctx, ports.FilterInput{Name: "clip.mkv", Size: 0})
	require.False(t, ok)
	require.Equal(t, ports.ExtensionExcluded, reason)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			_, _ = filter.ShouldHandle(ctx, ports.FilterInput{Name: "clip.mp4", Size: 1 << 20})
		}
	}()
	for range 20 {
		require.NoError(t, run.Reconfigure(ctx, ID, map[string]any{includeField: []string{"mkv"}}))
		require.NoError(t, run.Reconfigure(ctx, ID, map[string]any{includeField: []string{"mp4"}}))
	}
	wg.Wait()
	require.Equal(t, int64(1<<63-1), mbToBytes(1<<63-1))
}

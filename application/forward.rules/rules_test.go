package forwardrules

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	testChannel = "channel:1"
	testTarget  = "chat:2"
)

func ruleView(t *testing.T, rules []types.ForwardRule) config.View {
	t.Helper()
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	view, err := config.New(registry.Definitions(rte.AccountScope)[0].Manifest.Config, map[string]any{"rules": rules})
	require.NoError(t, err)
	return view
}

func TestFanOutDeduplicationAndAtomicHotUpdate(t *testing.T) {
	ctx := context.Background()
	r := &Rules{}
	first := types.ForwardRule{ID: "one", Enabled: true, Sources: []types.ChatRef{testChannel, "user:1"}, Targets: []types.ChatRef{testTarget, "chat:3"}, Mode: defaultMode}
	second := types.ForwardRule{ID: "two", Enabled: true, Sources: []types.ChatRef{testChannel}, Targets: []types.ChatRef{"chat:3", "self"}, Mode: "clone", Silent: true}
	require.NoError(t, r.Reconfigure(ctx, ruleView(t, []types.ForwardRule{first, second})))
	require.Len(t, r.Destinations(ctx, testChannel), 3)
	require.Len(t, r.Destinations(ctx, "user:1"), 2)
	require.Empty(t, r.Destinations(ctx, "chat:1"))
	before := r.Destinations(ctx, testChannel)
	require.Equal(t, defaultMode, before[1].Mode)
	before[0].Target = "user:99"
	require.Equal(t, types.ChatRef(testTarget), r.Destinations(ctx, testChannel)[0].Target)
	invalid := second
	invalid.Targets = []types.ChatRef{testChannel}
	require.Error(t, r.Reconfigure(ctx, ruleView(t, []types.ForwardRule{first, invalid})))
	require.Len(t, r.Destinations(ctx, testChannel), 3)
	second.Enabled = false
	commit, err := r.PrepareConfig(ctx, ruleView(t, []types.ForwardRule{first, second}))
	require.NoError(t, err)
	require.Len(t, r.Destinations(ctx, testChannel), 3, "prepare must not publish")
	commit()
	require.Len(t, r.Destinations(ctx, testChannel), 2)
}

func TestRejectCyclesAndConcurrentReaders(t *testing.T) {
	ctx := context.Background()
	r := &Rules{}
	a := types.ForwardRule{ID: "a", Enabled: true, Sources: []types.ChatRef{"chat:1"}, Targets: []types.ChatRef{testTarget}, Mode: defaultMode}
	b := types.ForwardRule{ID: "b", Enabled: true, Sources: []types.ChatRef{testTarget}, Targets: []types.ChatRef{"chat:1"}, Mode: defaultMode}
	require.Error(t, r.Reconfigure(ctx, ruleView(t, []types.ForwardRule{a, b})))
	on, off := ruleView(t, []types.ForwardRule{a}), ruleView(t, []types.ForwardRule{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for range 1000 {
				_ = r.Destinations(ctx, "chat:1")
			}
		})
	}
	for range 100 {
		require.NoError(t, r.Reconfigure(ctx, on))
		require.NoError(t, r.Reconfigure(ctx, off))
	}
	readers.Wait()
}

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

type runtimeTimeProbe struct{ calls atomic.Int32 }

func (p *runtimeTimeProbe) Query(context.Context, string, time.Duration) (types.TimeSample, error) {
	p.calls.Add(1)
	return types.TimeSample{Offset: time.Hour}, nil
}

func TestTimeComponentEnablementControlsProbingAndRTEBinding(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			ctx := config.WithSource(context.Background(), config.NewSource(config.DefaultConfig()))
			store := newStoppedComponentStore(t)
			for _, id := range []string{"downloader.local", "forwarder"} {
				saveComponent(t, store, id, false, nil)
			}
			saveComponent(t, store, "time.sync", enabled, map[string]any{"server": "clock.test"})
			probe := new(runtimeTimeProbe)
			manager := NewManager(ctx, nil, nil, Options{ComponentStore: store, TimeProbe: probe})
			t.Cleanup(manager.Shutdown)
			require.Equal(t, enabled, manager.hasRunnableModule(config.From(manager.parent)), "time sync can run as an independent headless component")
			clock := rte.ClockFrom(manager.parent)
			require.False(t, clock.Status().Synchronized)
			require.NoError(t, manager.reconciler.Reconcile(manager.parent, manager.managedUnits(config.From(manager.parent))))
			if enabled {
				require.Eventually(t, func() bool { return clock.Status().Synchronized }, time.Second, time.Millisecond)
				value, err := manager.ResolveComponentPort("time.sync", ports.ClockName)
				require.NoError(t, err)
				require.WithinDuration(t, clock.Now(), value.(ports.Clock).Now(), time.Second)
				require.EqualValues(t, 1, probe.calls.Load())
				require.WithinDuration(t, time.Now().Add(time.Hour), clock.Now(), time.Second)
			} else {
				require.Zero(t, probe.calls.Load())
				require.WithinDuration(t, time.Now(), clock.Now(), time.Second)
			}
			require.NoError(t, manager.shutdown())
			require.False(t, clock.Status().Synchronized)
			require.WithinDuration(t, time.Now(), clock.Now(), time.Second)
		})
	}
}

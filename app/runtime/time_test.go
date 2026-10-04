package runtime

import (
	"context"
	"errors"
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

func (p *runtimeTimeProbe) Query(_ context.Context, host string, _ time.Duration) (types.TimeSample, error) {
	if host != "ntp.aliyun.com" {
		return types.TimeSample{}, errors.New("offline")
	}
	p.calls.Add(1)
	return types.TimeSample{Offset: time.Hour}, nil
}

type runtimeProxyTimeProbe struct{ calls atomic.Int32 }

const runtimeTimeProxyField = "proxy"

func (*runtimeProxyTimeProbe) Query(context.Context, string, time.Duration) (types.TimeSample, error) {
	return types.TimeSample{}, errors.New("offline")
}

func (p *runtimeProxyTimeProbe) QueryViaProxy(_ context.Context, _ string, proxy string, _ time.Duration) (types.TimeSample, error) {
	p.calls.Add(1)
	if proxy != "http://127.0.0.1:1080" {
		return types.TimeSample{}, errors.New("unexpected startup proxy")
	}
	return types.TimeSample{Offset: time.Minute, Source: types.ClockSourceHTTPS, Server: "https://time.test/"}, nil
}

func TestTimeProxyPortUsesStartupConfigurationSnapshot(t *testing.T) {
	ctx := config.WithSource(context.Background(), config.NewSource(config.DefaultConfig()))
	store := newStoppedComponentStore(t)
	for _, id := range []string{"downloader.local", "forwarder"} {
		saveComponent(t, store, id, false, nil)
	}
	saveComponent(t, store, "time.sync", true, nil)
	saveComponent(t, store, "account.telegram", true, map[string]any{runtimeTimeProxyField: "http://127.0.0.1:1080"})
	probe := new(runtimeProxyTimeProbe)
	manager := NewManager(ctx, nil, nil, Options{ComponentStore: store, TimeProbe: probe})
	t.Cleanup(manager.Shutdown)
	// A saved edit must not change the proxy used by running synchronization.
	saveComponent(t, store, "account.telegram", true, map[string]any{runtimeTimeProxyField: "http://127.0.0.1:2080"})
	require.NoError(t, manager.reconciler.Reconcile(manager.parent, manager.managedUnits(config.From(manager.parent))))
	clock := rte.ClockFrom(manager.parent)
	require.Eventually(t, func() bool { return clock.Status().Synchronized }, time.Second, time.Millisecond)
	require.Positive(t, probe.calls.Load())
	require.Equal(t, types.ClockSourceHTTPS, clock.Status().Source)
	require.True(t, clock.Status().ViaProxy)
	require.Equal(t, "https://time.test/", clock.Status().Server)
	require.WithinDuration(t, time.Now().Add(time.Minute), clock.Now(), time.Second)
}

func TestTimeComponentEnablementControlsProbingAndRTEBinding(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			ctx := config.WithSource(context.Background(), config.NewSource(config.DefaultConfig()))
			store := newStoppedComponentStore(t)
			for _, id := range []string{"downloader.local", "forwarder"} {
				saveComponent(t, store, id, false, nil)
			}
			saveComponent(t, store, "time.sync", enabled, nil)
			probe := new(runtimeTimeProbe)
			manager := NewManager(ctx, nil, nil, Options{ComponentStore: store, TimeProbe: probe})
			t.Cleanup(manager.Shutdown)
			require.True(t, manager.hasRunnableModule(config.From(manager.parent)), "HTTP is always a runnable process service")
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

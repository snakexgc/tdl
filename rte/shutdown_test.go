package rte_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/eventbus"
)

func TestStopTimeoutPreservesResourcesAndCanRetry(t *testing.T) {
	const topic = "probe"
	for _, event := range []bool{false, true} {
		name := "runnable"
		if event {
			name = "event"
		}
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var stopped []string
			registry := rte.NewRegistry()
			require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}, func() rte.Component {
				return &component{init: func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }, stop: func() error { stopped = append(stopped, providerID); return nil }}
			}))
			worker := func(ctx context.Context) error { close(entered); <-ctx.Done(); <-release; return nil }
			require.NoError(t, registry.Register(manifest.Manifest{ID: consumerID, Requires: []manifest.Require{{Port: port()}}, Publishes: []string{topic}, Subscribes: []string{topic}}, func() rte.Component {
				return &component{init: func(k rte.Kernel) error {
					if !event {
						return k.Runnables.Run("worker", 0, 0, worker, nil)
					}
					_, err := k.Events.Subscribe(topic, 1, func(ctx context.Context, _ eventbus.Event) error { return worker(ctx) }, nil)
					if err != nil {
						return err
					}
					return k.Events.Publish(context.Background(), topic, nil)
				}, stop: func() error { stopped = append(stopped, consumerID); return nil }}
			}))
			host, err := registry.Build(types.DefaultAccount, nil, nil)
			require.NoError(t, err)
			host.Start(context.Background())
			t.Cleanup(func() { unblock(); require.NoError(t, host.Stop(context.Background())) })
			<-entered
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				err = host.Stop(ctx)
				cancel()
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Empty(t, stopped, "neither consumer nor provider may release resources")
				health := host.Health()
				require.NotEmpty(t, health.Events)
				for _, item := range health.Components {
					require.NotEqual(t, rte.Stopped, item.State)
				}
			}
			unblock()
			require.NoError(t, host.Stop(context.Background()))
			require.NoError(t, host.Stop(context.Background()))
			require.Equal(t, []string{consumerID, providerID}, stopped)
			for _, item := range host.Health().Components {
				require.Equal(t, rte.Stopped, item.State)
			}
		})
	}
}

func TestStopHookFailurePreservesProviderForRetry(t *testing.T) {
	registry := rte.NewRegistry()
	var stopped []string
	attempts := 0
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }, stop: func() error { stopped = append(stopped, providerID); return nil }}
	}))
	require.NoError(t, registry.Register(manifest.Manifest{ID: consumerID, Requires: []manifest.Require{{Port: port()}}}, func() rte.Component {
		return &component{stop: func() error {
			attempts++
			if attempts == 1 {
				return errors.New("cleanup incomplete")
			}
			stopped = append(stopped, consumerID)
			return nil
		}}
	}))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(context.Background())
	require.ErrorContains(t, host.Stop(context.Background()), "cleanup incomplete")
	require.Empty(t, stopped)
	require.NoError(t, host.Stop(context.Background()))
	require.Equal(t, []string{consumerID, providerID}, stopped)
}

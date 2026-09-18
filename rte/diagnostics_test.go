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

func TestHealthRemainsAvailableDuringLifecycleHooks(t *testing.T) {
	const startPhase = "start"
	for _, phase := range []string{startPhase, "stop"} {
		t.Run(phase, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			hook := func() error { close(entered); <-release; return nil }
			registry := rte.NewRegistry()
			c := &component{}
			state := rte.Starting
			if phase == startPhase {
				c.start = hook
			} else {
				c.stop = hook
				state = rte.Stopping
			}
			require.NoError(t, registry.Register(manifest.Manifest{ID: providerID}, func() rte.Component { return c }))
			host, err := registry.Build(types.DefaultAccount, nil, nil)
			require.NoError(t, err)
			if phase == "stop" {
				host.Start(context.Background())
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				if phase == startPhase {
					host.Start(context.Background())
				} else {
					_ = host.Stop(context.Background())
				}
			}()
			t.Cleanup(func() { unblock(); <-done; require.NoError(t, host.Stop(context.Background())) })
			<-entered
			read := make(chan rte.Health, 1)
			go func() { read <- host.Health() }()
			select {
			case health := <-read:
				require.Equal(t, state, health.Components[0].State)
			case <-time.After(time.Second):
				t.Fatal("health blocked behind lifecycle hook")
			}
		})
	}
}

func TestHealthRecordsUnhandledEventAndRunnableFailures(t *testing.T) {
	const topic = "diagnostic.test"
	registry := rte.NewRegistry()
	var events rte.Events
	require.NoError(t, registry.Register(manifest.Manifest{ID: "diagnostic", Publishes: []string{topic}, Subscribes: []string{topic}}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error {
			events = k.Events
			_, err := k.Events.Subscribe(topic, 1, func(context.Context, eventbus.Event) error { return errors.New("delivery failed") }, nil)
			if err != nil {
				return err
			}
			return k.Runnables.Run("worker", 0, 0, func(context.Context) error { panic("worker failed") }, nil)
		}}
	}))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(context.Background())
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	require.NoError(t, events.Publish(context.Background(), topic, nil))
	require.Eventually(t, func() bool { return len(host.Health().Events) == 2 }, time.Second, time.Millisecond)
	health := host.Health()
	require.Equal(t, types.DefaultAccount, health.Account)
	require.Len(t, health.Components, 1)
	require.Len(t, health.Components[0].Runnables, 1)
	require.Contains(t, health.Components[0].Runnables[0].LastError, "worker failed")
	require.Equal(t, uint64(1), health.Components[0].Runnables[0].Runs)
	operations := []string{health.Events[0].Operation, health.Events[1].Operation}
	require.ElementsMatch(t, []string{"event:" + topic, "runnable:worker"}, operations)
}

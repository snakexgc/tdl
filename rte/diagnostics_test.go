package rte_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
			c.init = func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }
			definition := manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}
			require.NoError(t, registry.Register(definition, func() rte.Component { return c }))
			host, err := registry.Build(types.DefaultAccount, nil, nil)
			require.NoError(t, err)
			catalog, err := rte.NewCatalog(rte.Definition{Manifest: definition, Scope: rte.AccountScope})
			require.NoError(t, err)
			directory := rte.NewDirectory(catalog, nil)
			require.NoError(t, directory.Bind("test", func() *rte.Runtime { return host }))
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
			queried := make(chan struct{})
			go func() {
				defer close(queried)
				assert.Equal(t, state, host.Statuses()[0].State)
				assert.Equal(t, state, host.Configurations()[0].State)
				assert.Equal(t, state, directory.Configurations(context.Background())[0].State)
				_, err := host.Resolve("greeting")
				assert.Error(t, err, "starting/stopping components must not publish available ports")
				_, err = directory.ResolveComponentPort(providerID, "greeting")
				assert.Error(t, err)
			}()
			t.Cleanup(func() { unblock(); <-queried })
			select {
			case <-queried:
			case <-time.After(time.Second):
				t.Fatal("configuration/status/port queries blocked behind lifecycle hook")
			}
		})
	}
}

func TestQueriesUsePublishedConfigurationWhilePreparing(t *testing.T) {
	ctx := context.Background()
	c := &directoryComponent{preparing: make(chan struct{}), unblock: make(chan struct{})}
	c.init = func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }
	m := manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}, Config: []manifest.ConfigField{
		{Name: directoryValueField, Type: manifest.Int, Default: 1},
	}}
	registry := rte.NewRegistry()
	require.NoError(t, registry.Register(m, func() rte.Component { return c }))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(ctx)
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: m, Scope: rte.AccountScope})
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, nil)
	require.NoError(t, directory.Bind("test", func() *rte.Runtime { return host }))
	done := make(chan struct{})
	var changeErr error
	go func() {
		defer close(done)
		changeErr = host.ReconfigureBatch(ctx, map[string]map[string]any{providerID: {directoryValueField: 2}})
	}()
	var once sync.Once
	unblock := func() { once.Do(func() { close(c.unblock) }) }
	t.Cleanup(func() { unblock(); <-done; require.NoError(t, host.Stop(ctx)) })
	<-c.preparing
	queried := make(chan struct{})
	go func() {
		defer close(queried)
		value, err := host.Resolve("greeting")
		assert.NoError(t, err)
		assert.Equal(t, greeter{}, value)
		value, err = directory.ResolveComponentPort(providerID, "greeting")
		assert.NoError(t, err)
		assert.Equal(t, greeter{}, value)
		for _, entries := range [][]rte.Configuration{host.Configurations(), directory.Configurations(ctx)} {
			assert.Equal(t, rte.Running, entries[0].State)
			data, err := json.Marshal(entries[0].Values)
			assert.NoError(t, err)
			assert.JSONEq(t, `{"value":1}`, string(data))
		}
	}()
	t.Cleanup(func() { unblock(); <-queried })
	select {
	case <-queried:
	case <-time.After(time.Second):
		t.Fatal("queries waited for configuration preparation")
	}
	unblock()
	<-done
	require.NoError(t, changeErr)
	for _, entries := range [][]rte.Configuration{host.Configurations(), directory.Configurations(ctx)} {
		data, err := json.Marshal(entries[0].Values)
		require.NoError(t, err)
		require.JSONEq(t, `{"value":2}`, string(data))
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

package rte_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/eventbus"
)

func TestScopedEventsDrainBeforeComponentStop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const topic = "task.finished"
	registry := rte.NewRegistry()
	var publisher rte.Events
	var stopped atomic.Bool
	received := make(chan types.AccountID, 1)
	consumer := &component{init: func(k rte.Kernel) error {
		_, err := k.Events.Subscribe("not-declared", 1, func(context.Context, eventbus.Event) error { return nil }, nil)
		require.Error(t, err)
		_, err = k.Events.Subscribe(topic, 1, func(ctx context.Context, e eventbus.Event) error {
			received <- e.Account
			<-ctx.Done()
			stopped.Store(true)
			return nil
		}, nil)
		return err
	}, stop: func() error {
		require.True(t, stopped.Load(), "event handlers must release resources before the Stop hook")
		return nil
	}}
	require.NoError(t, registry.Register(manifest.Manifest{ID: consumerID, Subscribes: []string{topic}}, func() rte.Component { return consumer }))
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Publishes: []string{topic}}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error { publisher = k.Events; return nil }}
	}))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(ctx)
	require.Error(t, publisher.Publish(ctx, "not-declared", nil))
	require.NoError(t, publisher.Publish(ctx, topic, map[string]string{"task": "one"}))
	select {
	case account := <-received:
		require.Equal(t, types.DefaultAccount, account)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, host.Stop(ctx))
	require.Error(t, publisher.Publish(ctx, topic, nil))
}

func TestFailedInitDrainsItsOwnSubscriptions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const topic = "self-test"
	registry := rte.NewRegistry()
	var handlerStopped atomic.Bool
	entered := make(chan struct{})
	failed := &component{init: func(k rte.Kernel) error {
		_, err := k.Events.Subscribe(topic, 1, func(ctx context.Context, _ eventbus.Event) error {
			close(entered)
			<-ctx.Done()
			handlerStopped.Store(true)
			return nil
		}, nil)
		if err != nil {
			return err
		}
		if err := k.Events.Publish(ctx, topic, nil); err != nil {
			return err
		}
		select {
		case <-entered:
		case <-ctx.Done():
			return ctx.Err()
		}
		return errors.New("initialization failed")
	}, stop: func() error {
		require.True(t, handlerStopped.Load())
		return nil
	}}
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Publishes: []string{topic}, Subscribes: []string{topic}}, func() rte.Component { return failed }))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Failed, host.Start(ctx)[0].State)
	require.NoError(t, host.Stop(ctx))
}

package rte_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestProductionGraphDrainsConsumersAndDoesNotOverlapAfterTimeout(t *testing.T) {
	ctx := context.Background()
	r := rte.NewReconciler(types.DefaultAccount)
	events := []string{}
	blocked := true
	unit := func(id string) rte.ManagedUnit {
		return rte.ManagedUnit{ID: id, Enabled: true, Revision: "one", Start: func(context.Context) error { events = append(events, "start "+id); return nil }, Stop: func(context.Context) error {
			events = append(events, "stop "+id)
			if id == "consumer" && blocked {
				return context.DeadlineExceeded
			}
			return nil
		}}
	}
	provider, consumer, independent := unit("provider"), unit("consumer"), unit("independent")
	consumer.Requires = []string{"provider"}
	require.NoError(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer, independent}))
	events = nil
	provider.Revision = "two"
	require.ErrorIs(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer, independent}), context.DeadlineExceeded)
	require.Equal(t, []string{stopConsumerEvent}, events, "owner must remain alive while a consumer is still draining")
	blocked = false
	events = nil
	require.NoError(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer, independent}))
	require.Equal(t, []string{stopConsumerEvent, stopProviderEvent, "start provider", "start consumer"}, events)
	events = nil
	provider.Enabled = false
	require.NoError(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer, independent}))
	require.Equal(t, []string{stopConsumerEvent, stopProviderEvent}, events)
	states := map[string]rte.State{}
	for _, status := range r.Health().Components {
		states[status.ID] = status.State
	}
	require.Equal(t, rte.Blocked, states["consumer"])
	require.Equal(t, rte.Running, states["independent"])
}

func TestProductionGraphRejectsInvalidGraphAndIsolatesStartFailure(t *testing.T) {
	r := rte.NewReconciler(types.DefaultAccount)
	starts := 0
	a := rte.ManagedUnit{ID: "a", Enabled: true, Requires: []string{"absent-provider"}, Start: func(context.Context) error { starts++; return errors.New("unavailable") }, Stop: func(context.Context) error { return nil }}
	require.Error(t, r.Reconcile(context.Background(), []rte.ManagedUnit{a}))
	require.Zero(t, starts)
	a.Requires = nil
	b := rte.ManagedUnit{ID: "b", Enabled: true, Start: func(context.Context) error { starts++; return nil }, Stop: func(context.Context) error { return nil }}
	require.Error(t, r.Reconcile(context.Background(), []rte.ManagedUnit{a, b}))
	require.Equal(t, 2, starts)
	states := r.Health().Components
	require.Equal(t, rte.Failed, states[0].State)
	require.Equal(t, rte.Running, states[1].State)
}

func TestProductionGraphRetainsPartiallyStartedResource(t *testing.T) {
	r := rte.NewReconciler(types.DefaultAccount)
	starts, stops := 0, 0
	failStop := true
	unit := rte.ManagedUnit{ID: "partial", Enabled: true, Running: func() bool { return false }, Start: func(context.Context) error { starts++; return errors.New("start failed after allocation") }, Stop: func(context.Context) error {
		stops++
		if failStop {
			return context.DeadlineExceeded
		}
		return nil
	}}
	require.Error(t, r.Reconcile(context.Background(), []rte.ManagedUnit{unit}))
	require.Equal(t, 1, starts)
	require.Equal(t, 1, stops)
	require.Error(t, r.Reconcile(context.Background(), []rte.ManagedUnit{unit}))
	require.Equal(t, 1, starts)
	require.Equal(t, 2, stops)
	failStop = false
	unit.Enabled = false
	require.NoError(t, r.Reconcile(context.Background(), []rte.ManagedUnit{unit}))
	require.Equal(t, 3, stops)
}

func TestProductionGraphDrainsOldDependenciesBeforeChangingGraph(t *testing.T) {
	r := rte.NewReconciler(types.DefaultAccount)
	events := []string{}
	unit := func(id string) rte.ManagedUnit {
		return rte.ManagedUnit{ID: id, Enabled: true, Start: func(context.Context) error { events = append(events, "start "+id); return nil }, Stop: func(context.Context) error { events = append(events, "stop "+id); return nil }}
	}
	provider, consumer := unit("p"), unit("c")
	consumer.Requires = []string{"p"}
	require.NoError(t, r.Reconcile(context.Background(), []rte.ManagedUnit{provider, consumer}))
	events = nil
	provider.Enabled = false
	consumer.Requires = nil
	require.NoError(t, r.Reconcile(context.Background(), []rte.ManagedUnit{provider, consumer}))
	require.Equal(t, []string{"stop c", "stop p", "start c"}, events)
}

const (
	stopConsumerEvent = "stop consumer"
	stopProviderEvent = "stop provider"
)

func TestProductionGraphRestartsConsumersAfterProviderExits(t *testing.T) {
	ctx := context.Background()
	r := rte.NewReconciler(types.DefaultAccount)
	events := []string{}
	running := false
	failStop := false
	provider := rte.ManagedUnit{
		ID: providerID, Enabled: true,
		Running: func() bool { return running },
		Start: func(context.Context) error {
			events = append(events, "start provider")
			running = true
			return nil
		},
		Stop: func(context.Context) error {
			events = append(events, stopProviderEvent)
			if failStop {
				return context.DeadlineExceeded
			}
			running = false
			return nil
		},
	}
	consumer := rte.ManagedUnit{
		ID: consumerID, Enabled: true, Requires: []string{providerID},
		Start: func(context.Context) error { events = append(events, "start consumer"); return nil },
		Stop:  func(context.Context) error { events = append(events, stopConsumerEvent); return nil },
	}
	require.NoError(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer}))
	running, failStop = false, true // The process exited, but its resources still need cleanup.
	events = nil
	require.ErrorIs(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer}), context.DeadlineExceeded)
	require.Equal(t, []string{stopConsumerEvent, stopProviderEvent}, events)
	failStop = false
	events = nil
	require.NoError(t, r.Reconcile(ctx, []rte.ManagedUnit{provider, consumer}))
	require.Equal(t, []string{stopProviderEvent, "start provider", "start consumer"}, events)
}

func TestProductionGraphCanceledReconcilePreservesRunningResources(t *testing.T) {
	r := rte.NewReconciler(types.DefaultAccount)
	stops := 0
	unit := rte.ManagedUnit{
		ID: providerID, Enabled: true,
		Start: func(context.Context) error { return nil },
		Stop:  func(context.Context) error { stops++; return nil },
	}
	require.NoError(t, r.Reconcile(context.Background(), []rte.ManagedUnit{unit}))
	before := r.Health()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, r.Reconcile(ctx, nil), context.Canceled)
	require.Zero(t, stops)
	require.Equal(t, before, r.Health())
}

package rte_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const testUnrelatedComponent = "unrelated"

func TestReconcileComponentsPreservesUnrelatedAndRebindsOptionalPorts(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	starts, stops := map[string]int{}, map[string]int{}
	provider := func() rte.Component {
		return &component{init: func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }, start: func() error { starts[providerID]++; return nil }, stop: func() error { stops[providerID]++; return nil }}
	}
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}, provider))
	for _, id := range []string{consumerID, testUnrelatedComponent} {
		m := manifest.Manifest{ID: id}
		if id == consumerID {
			m.Requires = []manifest.Require{{Port: port(), Optional: true}}
		}
		require.NoError(t, registry.Register(m, func() rte.Component {
			return &component{start: func() error { starts[id]++; return nil }, stop: func() error { stops[id]++; return nil }}
		}))
	}
	build := func(enabled bool) *rte.Runtime {
		run, err := registry.Build(types.DefaultAccount, map[string]bool{providerID: enabled, consumerID: true, testUnrelatedComponent: true}, nil)
		require.NoError(t, err)
		return run
	}
	run := build(true)
	run.Start(ctx)
	defer func() { require.NoError(t, run.Stop(ctx)) }()
	require.NoError(t, run.ReconcileComponents(ctx, build(false)))
	require.Equal(t, 1, starts[testUnrelatedComponent])
	require.Zero(t, stops[testUnrelatedComponent])
	require.Equal(t, 2, starts[consumerID])
	_, err := run.Resolve("greeting")
	require.Error(t, err)
	require.NoError(t, run.ReconcileComponents(ctx, build(true)))
	require.Equal(t, 2, starts[providerID])
	require.Equal(t, 3, starts[consumerID])
	require.Equal(t, 1, starts[testUnrelatedComponent])
	_, err = run.Resolve("greeting")
	require.NoError(t, err)
}

func TestReconcileComponentsConcurrentInspection(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	m := manifest.Manifest{ID: "toggle"}
	require.NoError(t, registry.Register(m, func() rte.Component { return &component{} }))
	build := func(enabled bool) *rte.Runtime {
		host, err := registry.Build(types.DefaultAccount, map[string]bool{m.ID: enabled}, nil)
		require.NoError(t, err)
		return host
	}
	host := build(true)
	host.Start(ctx)
	catalog, err := rte.NewCatalog(rte.Definition{Manifest: m, Scope: rte.AccountScope})
	require.NoError(t, err)
	directory := rte.NewDirectory(catalog, config.NewStore(t.TempDir()))
	require.NoError(t, directory.Bind("host", func() *rte.Runtime { return host }))
	readCtx, cancel := context.WithCancel(ctx)
	var readers sync.WaitGroup
	for _, read := range []func(){func() { host.Health() }, func() { directory.Configurations(ctx) }} {
		readers.Go(func() {
			for readCtx.Err() == nil {
				read()
				runtime.Gosched()
			}
		})
	}
	t.Cleanup(func() { cancel(); readers.Wait(); require.NoError(t, host.Stop(ctx)) })
	for i := 0; i < 100; i++ {
		require.NoError(t, host.ReconcileComponents(ctx, build(i%2 == 0)))
		runtime.Gosched()
	}
}

func TestReconcileComponentsReportsStartFailureAndCanRetry(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	fail := true
	starts := 0
	require.NoError(t, registry.Register(manifest.Manifest{ID: "failing"}, func() rte.Component {
		return &component{start: func() error {
			if fail {
				return errors.New("device unavailable")
			}
			return nil
		}}
	}))
	require.NoError(t, registry.Register(manifest.Manifest{ID: testUnrelatedComponent}, func() rte.Component {
		return &component{start: func() error { starts++; return nil }}
	}))
	build := func(enabled bool) *rte.Runtime {
		host, err := registry.Build(types.DefaultAccount, map[string]bool{"failing": enabled, testUnrelatedComponent: true}, nil)
		require.NoError(t, err)
		return host
	}
	host := build(false)
	host.Start(ctx)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	require.ErrorContains(t, host.ReconcileComponents(ctx, build(true)), "device unavailable")
	require.Equal(t, 1, starts)
	fail = false
	require.NoError(t, host.ReconcileComponents(ctx, build(true)))
	require.Equal(t, 1, starts)
}

func TestReconcileComponentsRetainsProviderUntilConsumerDrains(t *testing.T) {
	ctx := context.Background()
	registry := rte.NewRegistry()
	fail := true
	providerStops := 0
	require.NoError(t, registry.Register(manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error { return k.Provide("greeting", greeter{}) }, stop: func() error { providerStops++; return nil }}
	}))
	require.NoError(t, registry.Register(manifest.Manifest{ID: consumerID, Requires: []manifest.Require{{Port: port()}}}, func() rte.Component {
		return &component{stop: func() error {
			if fail {
				return errors.New("still draining")
			}
			return nil
		}}
	}))
	run, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	run.Start(ctx)
	empty, err := registry.Build(types.DefaultAccount, map[string]bool{}, nil)
	require.NoError(t, err)
	require.ErrorContains(t, run.ReconcileComponents(ctx, empty), "still draining")
	require.Zero(t, providerStops)
	fail = false
	require.NoError(t, run.ReconcileComponents(ctx, empty))
	require.Equal(t, 1, providerStops)
	require.NoError(t, run.Stop(ctx))
}

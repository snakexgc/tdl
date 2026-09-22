package rte_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

type (
	greeting interface{ Hello() string }
	greeter  struct{}
)

const (
	providerID     = "provider"
	nilFactoryCase = "nil-factory"
	limitField     = "limit"
	consumerID     = "consumer"
	optionalID     = "optional"
)

func (greeter) Hello() string { return "hello" }

type component struct {
	init    func(rte.Kernel) error
	start   func() error
	stop    func() error
	changes int
}

func (c *component) Init(_ context.Context, k rte.Kernel) error {
	if c.init != nil {
		return c.init(k)
	}
	return nil
}

func (c *component) Start(context.Context) error {
	if c.start != nil {
		return c.start()
	}
	return nil
}

func (c *component) Stop(context.Context) error {
	if c.stop != nil {
		return c.stop()
	}
	return nil
}
func (c *component) Reconfigure(context.Context, config.View) error { c.changes++; return nil }
func factory() rte.Component                                        { return &component{} }
func port() manifest.Port                                           { return manifest.PortOf[greeting]("greeting", 1, 0) }

func TestAssemblyRejectsInvalidGraphsBeforeFactories(t *testing.T) {
	for _, name := range []string{"missing", "duplicate", "type", "version", "cycle"} {
		t.Run(name, func(t *testing.T) {
			r := rte.NewRegistry()
			provider := manifest.Manifest{ID: providerID, Provides: []manifest.Port{port()}}
			consumer := manifest.Manifest{ID: consumerID, Requires: []manifest.Require{{Port: port()}}}
			switch name {
			case "missing":
				provider.Provides = nil
			case "duplicate":
				consumer.Provides = []manifest.Port{port()}
			case "type":
				consumer.Requires[0].Type = manifest.PortOf[error]("greeting", 1, 0).Type
			case "version":
				consumer.Requires[0].Minor = 1
			case "cycle":
				provider.Requires = []manifest.Require{{Port: port()}}
			}
			created := 0
			makeComponent := func() rte.Component { created++; return factory() }
			require.NoError(t, r.Register(provider, makeComponent))
			require.NoError(t, r.Register(consumer, makeComponent))
			_, err := r.Build(types.DefaultAccount, nil, nil)
			require.Error(t, err)
			require.Zero(t, created)
		})
	}
}

func TestLifecycleTopologyScopedPortsAndConfigIsolation(t *testing.T) {
	r := rte.NewRegistry()
	var order []string
	var providerKernel rte.Kernel
	p := &component{init: func(k rte.Kernel) error { providerKernel = k; return k.Provide("greeting", greeter{}) }, start: func() error { order = append(order, providerID); return nil }, stop: func() error { order = append(order, "stop-provider"); return nil }}
	c := &component{init: func(k rte.Kernel) error {
		value, err := k.Resolve("greeting")
		if err != nil {
			return err
		}
		require.Equal(t, "hello", value.(greeting).Hello())
		_, err = k.Resolve("undeclared")
		require.Error(t, err)
		return nil
	}, start: func() error { order = append(order, consumerID); return nil }, stop: func() error { order = append(order, "stop-consumer"); return nil }}
	require.NoError(t, r.Register(manifest.Manifest{ID: "z-provider", Provides: []manifest.Port{port()}}, func() rte.Component { return p }))
	require.NoError(t, r.Register(manifest.Manifest{ID: "a-consumer", Requires: []manifest.Require{{Port: port()}}, Config: []manifest.ConfigField{{Name: limitField, Type: manifest.Int, Default: 1}}}, func() rte.Component { return c }))
	run, err := r.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	for _, status := range run.Start(context.Background()) {
		require.Equal(t, rte.Running, status.State)
	}
	require.Equal(t, []string{providerID, consumerID}, order)
	require.ErrorContains(t, providerKernel.Provide("greeting", greeter{}), "Init")
	require.NoError(t, run.Reconfigure(context.Background(), "a-consumer", map[string]any{limitField: 2}))
	require.NoError(t, run.Reconfigure(context.Background(), "a-consumer", map[string]any{limitField: 2}))
	require.Equal(t, 1, c.changes)
	require.Zero(t, p.changes)
	require.NoError(t, run.Stop(context.Background()))
	require.NoError(t, run.Stop(context.Background()))
	require.Equal(t, []string{providerID, consumerID, "stop-consumer", "stop-provider"}, order)
	_, err = run.Resolve("greeting")
	require.Error(t, err)
}

func TestFailureIsolationAndCleanup(t *testing.T) {
	for _, kind := range []string{"init", "panic", "missing-binding", "wrong-binding", nilFactoryCase, "start"} {
		t.Run(kind, func(t *testing.T) {
			r := rte.NewRegistry()
			cleaned := 0
			bad := &component{init: func(k rte.Kernel) error {
				switch kind {
				case "init":
					return errors.New("broken")
				case "panic":
					panic("broken")
				case "missing-binding":
					return nil
				case "wrong-binding":
					return k.Provide("greeting", errors.New("wrong"))
				}
				return k.Provide("greeting", greeter{})
			}, start: func() error { return errors.New("start failed") }, stop: func() error { cleaned++; return nil }}
			require.NoError(t, r.Register(manifest.Manifest{ID: "bad", Provides: []manifest.Port{port()}}, func() rte.Component {
				if kind == nilFactoryCase {
					return (*component)(nil)
				}
				return bad
			}))
			require.NoError(t, r.Register(manifest.Manifest{ID: "dependent", Requires: []manifest.Require{{Port: port()}}}, factory))
			require.NoError(t, r.Register(manifest.Manifest{ID: "independent"}, factory))
			run, err := r.Build(types.DefaultAccount, nil, nil)
			require.NoError(t, err)
			statuses := run.Start(context.Background())
			require.Equal(t, rte.Failed, statuses[0].State)
			require.Equal(t, rte.Blocked, statuses[1].State)
			require.Equal(t, rte.Running, statuses[2].State)
			require.NoError(t, run.Stop(context.Background()))
			if kind == nilFactoryCase {
				require.Zero(t, cleaned)
			} else {
				require.Equal(t, 1, cleaned)
			}
		})
	}
}

func TestEmptyAndOptionalComponents(t *testing.T) {
	r := rte.NewRegistry()
	run, err := r.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Empty(t, run.Start(context.Background()))
	require.NoError(t, run.Stop(context.Background()))
	require.NoError(t, r.Register(manifest.Manifest{ID: optionalID, Requires: []manifest.Require{{Port: port(), Optional: true}}}, factory))
	run, err = r.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, run.Start(context.Background())[0].State)
	require.NoError(t, run.Stop(context.Background()))
	run, err = r.Build(types.DefaultAccount, map[string]bool{optionalID: false}, nil)
	require.NoError(t, err)
	require.Empty(t, run.Start(context.Background()))
	require.NoError(t, run.Stop(context.Background()))
	require.Error(t, r.Register(manifest.Manifest{ID: optionalID}, factory))
}

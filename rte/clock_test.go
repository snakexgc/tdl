package rte_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time          { return c.at }
func (fixedClock) Status() types.ClockStatus { return types.ClockStatus{Synchronized: true} }

func TestKernelAndAdapterShareClockAcrossLateBinding(t *testing.T) {
	binding := new(rte.ClockBinding)
	ctx := rte.WithClock(context.Background(), binding)
	registry := rte.NewRegistry()
	var componentClock ports.Clock
	require.NoError(t, registry.Register(manifest.Manifest{ID: "consumer"}, func() rte.Component {
		return &component{init: func(k rte.Kernel) error { componentClock = k.Clock; return nil }}
	}))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(ctx)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	require.WithinDuration(t, time.Now(), componentClock.Now(), time.Second)
	corrected := fixedClock{time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)}
	binding.Bind(corrected)
	require.Equal(t, corrected.Now(), componentClock.Now())
	require.Equal(t, corrected.Now(), rte.Now(ctx))
	require.True(t, componentClock.Status().Synchronized)
	binding.Bind(nil)
	require.WithinDuration(t, time.Now(), componentClock.Now(), time.Second)
}

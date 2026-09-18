package schedule

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDynamicPeriodWakesIdleTaskAndRecoversAfterError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	group := New(ctx)
	t.Cleanup(func() { require.NoError(t, group.Stop(context.Background())) })
	var period atomic.Int64
	period.Store(int64(time.Hour))
	changed := make(chan struct{}, 1)
	reports := make(chan error, 1)
	var runs atomic.Int32
	require.NoError(t, group.RunDynamic("cleanup", time.Hour, func() time.Duration { return time.Duration(period.Load()) }, changed,
		func(context.Context) error {
			if runs.Add(1) == 1 {
				return errors.New("temporary failure")
			}
			return nil
		},
		func(err error) { reports <- err }))
	period.Store(int64(time.Millisecond))
	changed <- struct{}{}
	select {
	case err := <-reports:
		require.ErrorContains(t, err, "temporary failure")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Eventually(t, func() bool { return runs.Load() >= 2 }, time.Second, time.Millisecond)
	require.NoError(t, group.Stop(context.Background()))
	require.GreaterOrEqual(t, group.Statuses()[0].Runs, uint64(2))
}

func TestPeriodicOverrunIsObservableAndStops(t *testing.T) {
	group := New(context.Background())
	t.Cleanup(func() { require.NoError(t, group.Stop(context.Background())) })
	require.NoError(t, group.Run("slow", 0, time.Millisecond, func(ctx context.Context) error { <-ctx.Done(); return nil }, nil))
	require.Eventually(t, func() bool { states := group.Statuses(); return len(states) == 1 && states[0].Overdue }, time.Second, time.Millisecond)
	require.NoError(t, group.Stop(context.Background()))
	state := group.Statuses()[0]
	require.False(t, state.Running)
	require.False(t, state.Overdue)
	require.Equal(t, uint64(1), state.Runs)
}

func TestRunnableCancellationAndIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	group := New(ctx)
	reported := make(chan error, 1)
	require.NoError(t, group.Run("panic", 0, 0, func(context.Context) error { panic("test") }, func(err error) { reported <- err }))
	var active atomic.Int32
	entered := make(chan struct{})
	require.NoError(t, group.Run("worker", 0, time.Millisecond, func(ctx context.Context) error {
		active.Add(1)
		defer active.Add(-1)
		close(entered)
		<-ctx.Done()
		return nil
	}, nil))
	require.Error(t, group.Run("worker", 0, 0, func(context.Context) error { return nil }, nil))
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-reported:
		require.ErrorContains(t, err, "panic")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, group.Stop(ctx))
	require.Zero(t, active.Load())
	require.Error(t, group.Run("late", 0, 0, func(context.Context) error { return nil }, nil))
}

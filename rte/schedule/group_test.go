package schedule

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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

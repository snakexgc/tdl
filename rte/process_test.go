package rte

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessStopTimeoutCannotCreateOverlappingInstance(t *testing.T) {
	p := NewProcess(context.Background(), "alice", "transport")
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	started, err := p.Start(func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}, Recovery{})
	require.NoError(t, err)
	require.True(t, started)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, p.Stop(ctx), context.DeadlineExceeded)
	<-canceled
	started, err = p.Start(func(context.Context) error { t.Error("overlapping instance"); return nil }, Recovery{})
	require.NoError(t, err)
	require.False(t, started)
	require.True(t, p.Running())
	require.Equal(t, Stopping, p.Health().Components[0].State)
	close(release)
	require.NoError(t, p.Stop(context.Background()))
	require.False(t, p.Running())
	started, err = p.Start(func(ctx context.Context) error { <-ctx.Done(); return nil }, Recovery{})
	require.NoError(t, err)
	require.True(t, started)
	require.NoError(t, p.Stop(context.Background()))
}

func TestProcessRecoveryBudgetAndDiagnosticIsolation(t *testing.T) {
	transient := errors.New("transient transport failure")
	p := NewProcess(context.Background(), "alice", "transport")
	var calls atomic.Int32
	started, err := p.Start(func(context.Context) error { calls.Add(1); return transient }, Recovery{
		MaxRestarts: 2, Delay: time.Millisecond, Retryable: func(err error) bool { return errors.Is(err, transient) },
	})
	require.NoError(t, err)
	require.True(t, started)
	require.Eventually(t, func() bool { return !p.Running() }, time.Second, time.Millisecond)
	require.EqualValues(t, 3, calls.Load())
	require.ErrorIs(t, p.LastError(), transient)
	health := p.Health()
	require.Equal(t, Failed, health.Components[0].State)
	require.Len(t, health.Events, 3)
	for _, event := range health.Events {
		require.EqualValues(t, "alice", event.Account)
	}
	other := NewProcess(context.Background(), "bob", "transport")
	require.Empty(t, other.Health().Events)
	require.NoError(t, p.Stop(context.Background()))
}

func TestProcessPanicIsDiagnosedAndCancellationStopsRecovery(t *testing.T) {
	p := NewProcess(context.Background(), "alice", "transport")
	_, err := p.Start(func(context.Context) error { panic("broken adapter") }, Recovery{})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return !p.Running() }, time.Second, time.Millisecond)
	require.ErrorContains(t, p.LastError(), "broken adapter")
	var calls atomic.Int32
	_, err = p.Start(func(context.Context) error { calls.Add(1); return errors.New("retry") }, Recovery{
		MaxRestarts: 100, Delay: time.Hour, Retryable: func(error) bool { return true },
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return calls.Load() > 0 }, time.Second, time.Millisecond)
	require.NoError(t, p.Stop(context.Background()))
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, p.LastError())
}

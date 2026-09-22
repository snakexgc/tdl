package comif

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReconfigureKeepsExistingLeasesAndWakesWaiters(t *testing.T) {
	s := NewScheduler(2, 2)
	one := mustAcquireTask(t, s, "one", 2)
	two := mustAcquireTask(t, s, "two", 2)
	chunks := mustAcquireChunks(t, one, 2)
	s.Reconfigure(1, 1)
	require.Equal(t, 1, one.Capacity())
	require.Equal(t, 2, s.Snapshots()[0].ActiveChunks)
	assertChunkBlocked(t, two)
	chunks[0].Release()
	assertChunkBlocked(t, two)
	chunks[1].Release()
	chunk := mustAcquireChunks(t, two, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := s.Acquire(ctx, "three", 3)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	one.Release()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	_, err = s.Acquire(ctx2, "three", 3)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	waiting := acquireChunkAsync(two)
	s.Reconfigure(3, 2)
	next := requireChunkResult(t, waiting)
	three := mustAcquireTask(t, s, "three", 3)
	next.Release()
	releaseChunks(chunk)
	two.Release()
	three.Release()
}

func TestFileLimitIncreaseWakesBlockedAcquisition(t *testing.T) {
	s := NewScheduler(1, 1)
	one := mustAcquireTask(t, s, "one", 2)
	defer one.Release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan *TaskLease, 1)
	go func() { lease, _ := s.Acquire(ctx, "two", 2); done <- lease }()
	s.Reconfigure(2, 1)
	select {
	case lease := <-done:
		require.NotNil(t, lease)
		lease.Release()
	case <-ctx.Done():
		t.Fatal("increased quota did not wake waiter")
	}
}

package comif

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func waitForFiles(t *testing.T, scheduler *Scheduler, count int) {
	t.Helper()
	require.Eventually(t, func() bool {
		scheduler.fileTokens.mu.Lock()
		defer scheduler.fileTokens.mu.Unlock()
		return len(scheduler.fileTokens.waiters) == count
	}, time.Second, time.Millisecond)
}

func queuedFile(ctx context.Context, scheduler *Scheduler, id string, priority int) <-chan *TaskLease {
	result := make(chan *TaskLease, 1)
	go func() { lease, _ := scheduler.AcquireWithPriority(ctx, id, 2, priority); result <- lease }()
	return result
}

func receiveFile(t *testing.T, result <-chan *TaskLease) *TaskLease {
	t.Helper()
	select {
	case lease := <-result:
		require.NotNil(t, lease)
		return lease
	case <-time.After(time.Second):
		t.Fatal("file was not admitted")
		return nil
	}
}

func TestReactionFilesWinSharedFileBudgetAndListenerFIFOIsPreserved(t *testing.T) {
	s := NewScheduler(1, 3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	busy := mustAcquireTask(t, s, "already-running", 2)
	first := queuedFile(ctx, s, "listen-first", 0)
	waitForFiles(t, s, 1)
	second := queuedFile(ctx, s, "listen-second", 0)
	waitForFiles(t, s, 2)
	reaction := queuedFile(ctx, s, "reaction", 1)
	waitForFiles(t, s, 3)
	busy.Release()
	high := receiveFile(t, reaction)
	require.Equal(t, 3, high.Capacity())
	select {
	case <-first:
		t.Fatal("listener bypassed the shared file limit")
	default:
	}
	high.Release()
	low := receiveFile(t, first)
	select {
	case <-second:
		t.Fatal("equal-priority file bypassed FIFO or shared file limit")
	default:
	}
	low.Release()
	receiveFile(t, second).Release()
}

func TestQueuedListenerPromotionAndCancellationDoNotLeakPermits(t *testing.T) {
	s := NewScheduler(1, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	busy := mustAcquireTask(t, s, "busy", 2)
	first := queuedFile(ctx, s, "first-listener", 0)
	waitForFiles(t, s, 1)
	promoted := queuedFile(ctx, s, "also-requested-by-reaction", 0)
	waitForFiles(t, s, 2)
	s.Prioritize("also-requested-by-reaction", 1)
	canceled, stop := context.WithCancel(ctx)
	third := queuedFile(canceled, s, "canceled-reaction", 1)
	waitForFiles(t, s, 3)
	stop()
	select {
	case lease := <-third:
		require.Nil(t, lease)
	case <-time.After(time.Second):
		t.Fatal("canceled waiter did not return")
	}
	waitForFiles(t, s, 2)
	busy.Release()
	receiveFile(t, promoted).Release()
	receiveFile(t, first).Release()
	s.fileTokens.mu.Lock()
	defer s.fileTokens.mu.Unlock()
	require.Zero(t, s.fileTokens.used)
	require.Empty(t, s.fileTokens.waiters)
}

func TestReactionChunksWinSharedDCCapacityWithoutCancelingActiveChunks(t *testing.T) {
	s := NewScheduler(2, 1)
	listen := mustAcquireTask(t, s, "listener", 2)
	reaction, err := s.AcquireWithPriority(context.Background(), "reaction", 2, 1)
	require.NoError(t, err)
	defer listen.Release()
	defer reaction.Release()
	busy := mustAcquireChunks(t, listen, 1)
	low := acquireChunkAsync(listen)
	require.Eventually(t, func() bool { return s.Snapshots()[0].QueuedRequests == 1 }, time.Second, time.Millisecond)
	high := acquireChunkAsync(reaction)
	require.Eventually(t, func() bool { return s.Snapshots()[0].QueuedRequests == 2 }, time.Second, time.Millisecond)
	assertNoChunkResult(t, high)
	busy[0].Release()
	next := requireChunkResult(t, high)
	assertNoChunkResult(t, low)
	require.Equal(t, 1, s.Snapshots()[0].ActiveChunks)
	next.Release()
	requireChunkResult(t, low).Release()
}

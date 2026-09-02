package transfer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSchedulerDistributesCapacityByTask(t *testing.T) {
	s := NewScheduler(10, 8)
	one := mustAcquireTask(t, s, "one", 2)
	oneChunks := mustAcquireChunks(t, one, 8)
	two := mustAcquireTask(t, s, "two", 2)
	releaseChunks(oneChunks)

	oneChunks = mustAcquireChunks(t, one, 4)
	twoChunks := mustAcquireChunks(t, two, 4)
	assertChunkBlocked(t, one)
	assertChunkBlocked(t, two)
	releaseChunks(oneChunks)
	releaseChunks(twoChunks)

	three := mustAcquireTask(t, s, "three", 2)
	oneChunks = mustAcquireChunks(t, one, 3)
	twoChunks = mustAcquireChunks(t, two, 3)
	threeChunks := mustAcquireChunks(t, three, 2)
	assertChunkBlocked(t, one)
	assertChunkBlocked(t, two)
	assertChunkBlocked(t, three)

	releaseChunks(oneChunks)
	releaseChunks(twoChunks)
	releaseChunks(threeChunks)
	one.Release()
	two.Release()
	three.Release()
}

func TestSchedulerRedistributesUnusedShareWithoutLeavingDCIdle(t *testing.T) {
	s := NewScheduler(2, 4)
	one := mustAcquireTask(t, s, "one", 2)
	two := mustAcquireTask(t, s, "two", 2)

	oneChunks := mustAcquireChunks(t, one, 1)
	// Task one has no more demand. Task two must be allowed to consume every
	// remaining lane instead of being capped at a static 2/2 fair share.
	twoChunks := mustAcquireChunks(t, two, 3)
	require.Equal(t, 4, s.Snapshots()[0].ActiveChunks)
	assertChunkBlocked(t, two)

	releaseChunks(oneChunks)
	releaseChunks(twoChunks)
	one.Release()
	two.Release()
}

func TestSchedulerPrioritizesRangesByFileFIFO(t *testing.T) {
	s := NewScheduler(2, 2)
	one := mustAcquireTask(t, s, "one", 2)
	two := mustAcquireTask(t, s, "two", 2)
	oneChunks := mustAcquireChunks(t, one, 2)

	oneWaiting := acquireChunkAsync(one)
	require.Eventually(t, func() bool {
		return s.Snapshots()[0].QueuedRequests == 1
	}, time.Second, time.Millisecond)
	twoWaiting := acquireChunkAsync(two)
	require.Eventually(t, func() bool {
		return s.Snapshots()[0].QueuedRequests == 2
	}, time.Second, time.Millisecond)

	oneChunks[0].Release()
	oneNext := requireChunkResult(t, oneWaiting)
	assertNoChunkResult(t, twoWaiting)

	oneChunks[1].Release()
	twoNext := requireChunkResult(t, twoWaiting)
	oneNext.Release()
	twoNext.Release()
	one.Release()
	two.Release()
}

func TestSchedulerQueuesFilesBeyondDCCapacityFIFO(t *testing.T) {
	s := NewScheduler(10, 2)
	one := mustAcquireTask(t, s, "one", 4)
	two := mustAcquireTask(t, s, "two", 4)
	three := mustAcquireTask(t, s, "three", 4)
	oneChunk := mustAcquireChunks(t, one, 1)
	twoChunk := mustAcquireChunks(t, two, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := three.AcquireChunk(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	releaseChunks(oneChunk)
	one.Release()
	threeChunk := mustAcquireChunks(t, three, 1)
	releaseChunks(twoChunk)
	releaseChunks(threeChunk)
	two.Release()
	three.Release()
}

func TestSchedulerSeparatesDCBudgets(t *testing.T) {
	s := NewScheduler(4, 2)
	dcTwo := mustAcquireTask(t, s, "dc-two", 2)
	dcFour := mustAcquireTask(t, s, "dc-four", 4)
	twoChunks := mustAcquireChunks(t, dcTwo, 2)
	fourChunks := mustAcquireChunks(t, dcFour, 2)

	snapshots := s.Snapshots()
	require.Len(t, snapshots, 2)
	require.Equal(t, 2, snapshots[0].ActiveChunks)
	require.Equal(t, 2, snapshots[1].ActiveChunks)

	releaseChunks(twoChunks)
	releaseChunks(fourChunks)
	dcTwo.Release()
	dcFour.Release()
}

func TestSchedulerSharesOneAllocationAcrossTaskRequests(t *testing.T) {
	s := NewScheduler(2, 2)
	first := mustAcquireTask(t, s, "same", 2)
	second := mustAcquireTask(t, s, "same", 2)
	chunks := append(mustAcquireChunks(t, first, 1), mustAcquireChunks(t, second, 1)...)
	assertChunkBlocked(t, first)
	assertChunkBlocked(t, second)
	releaseChunks(chunks)
	first.Release()
	second.Release()
}

func TestSchedulerConvergesAfterNewTaskJoinsWithoutPreemption(t *testing.T) {
	s := NewScheduler(2, 4)
	one := mustAcquireTask(t, s, "one", 2)
	oneChunks := mustAcquireChunks(t, one, 4)
	two := mustAcquireTask(t, s, "two", 2)

	twoFirst := acquireChunkAsync(two)
	assertNoChunkResult(t, twoFirst)
	oneChunks[0].Release()
	first := requireChunkResult(t, twoFirst)

	twoSecond := acquireChunkAsync(two)
	assertNoChunkResult(t, twoSecond)
	oneChunks[1].Release()
	second := requireChunkResult(t, twoSecond)

	// The two already-running lanes are not preempted, while newly freed lanes
	// converge to the new 2/2 allocation.
	require.Equal(t, 2, one.state.inFlight)
	require.Equal(t, 2, two.state.inFlight)
	oneChunks[2].Release()
	oneChunks[3].Release()
	first.Release()
	second.Release()
	one.Release()
	two.Release()
}

func TestSchedulerCancellationRemovesQueuedChunk(t *testing.T) {
	s := NewScheduler(2, 1)
	lease := mustAcquireTask(t, s, "task", 2)
	chunk := mustAcquireChunks(t, lease, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := lease.AcquireChunk(ctx)
	require.ErrorIs(t, err, context.Canceled)
	releaseChunks(chunk)

	next := mustAcquireChunks(t, lease, 1)
	releaseChunks(next)
	lease.Release()
}

func TestSchedulerCancellationWhileWaitingForFileSlotDoesNotLeak(t *testing.T) {
	s := NewScheduler(1, 2)
	blocking := mustAcquireTask(t, s, "blocking", 2)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := s.Acquire(ctx, "canceled", 2)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	blocking.Release()
	next := mustAcquireTask(t, s, "next", 2)
	chunks := mustAcquireChunks(t, next, 2)
	releaseChunks(chunks)
	next.Release()
}

func TestSchedulerTaskReleaseWakesQueuedChunk(t *testing.T) {
	s := NewScheduler(1, 1)
	lease := mustAcquireTask(t, s, "task", 2)
	chunk := mustAcquireChunks(t, lease, 1)[0]

	errCh := make(chan error, 1)
	go func() {
		_, err := lease.AcquireChunk(context.Background())
		errCh <- err
	}()
	require.Eventually(t, func() bool {
		return s.Snapshots()[0].QueuedRequests == 1
	}, time.Second, time.Millisecond)
	lease.Release()

	select {
	case err := <-errCh:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("queued chunk did not wake after task release")
	}
	chunk.Release()
}

func mustAcquireTask(t *testing.T, s *Scheduler, taskID string, dc int) *TaskLease {
	t.Helper()
	lease, err := s.Acquire(context.Background(), taskID, dc)
	require.NoError(t, err)
	return lease
}

func mustAcquireChunks(t *testing.T, lease *TaskLease, count int) []*ChunkLease {
	t.Helper()
	chunks := make([]*ChunkLease, 0, count)
	for range count {
		chunk, err := lease.AcquireChunk(context.Background())
		require.NoError(t, err)
		chunks = append(chunks, chunk)
	}
	return chunks
}

func assertChunkBlocked(t *testing.T, lease *TaskLease) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := lease.AcquireChunk(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func acquireChunkAsync(lease *TaskLease) <-chan *ChunkLease {
	result := make(chan *ChunkLease, 1)
	go func() {
		chunk, _ := lease.AcquireChunk(context.Background())
		result <- chunk
	}()
	return result
}

func assertNoChunkResult(t *testing.T, result <-chan *ChunkLease) {
	t.Helper()
	select {
	case <-result:
		t.Fatal("chunk was granted before capacity became available")
	case <-time.After(30 * time.Millisecond):
	}
}

func requireChunkResult(t *testing.T, result <-chan *ChunkLease) *ChunkLease {
	t.Helper()
	select {
	case chunk := <-result:
		require.NotNil(t, chunk)
		return chunk
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for chunk grant")
		return nil
	}
}

func releaseChunks(chunks []*ChunkLease) {
	for _, chunk := range chunks {
		chunk.Release()
	}
}

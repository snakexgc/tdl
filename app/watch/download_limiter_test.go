package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDownloadLimiterLimitsConcurrentFiles(t *testing.T) {
	t.Parallel()

	limiter := newDownloadLimiter(2, 4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	releaseA, err := limiter.Acquire(ctx, "file-a")
	require.NoError(t, err)

	releaseB, err := limiter.Acquire(ctx, "file-b")
	require.NoError(t, err)
	defer releaseB.Release()

	acquired := make(chan *downloadLease, 1)
	errCh := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(ctx, "file-c")
		if err != nil {
			errCh <- err
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release.Release()
		t.Fatal("third file should wait for a free file slot")
	case err := <-errCh:
		t.Fatalf("unexpected acquire error: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	releaseA.Release()

	select {
	case release := <-acquired:
		release.Release()
	case err := <-errCh:
		t.Fatalf("unexpected acquire error after releasing slot: %v", err)
	case <-time.After(time.Second):
		t.Fatal("third file did not acquire slot after release")
	}
}

func TestDownloadLimiterLimitsPerFileRequests(t *testing.T) {
	t.Parallel()

	limiter := newDownloadLimiter(2, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	releaseOne, err := limiter.Acquire(ctx, "file-a")
	require.NoError(t, err)

	releaseTwo, err := limiter.Acquire(ctx, "file-a")
	require.NoError(t, err)
	defer releaseTwo.Release()

	acquired := make(chan *downloadLease, 1)
	errCh := make(chan error, 1)
	go func() {
		release, err := limiter.Acquire(ctx, "file-a")
		if err != nil {
			errCh <- err
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release.Release()
		t.Fatal("third request for same file should wait for a per-file slot")
	case err := <-errCh:
		t.Fatalf("unexpected acquire error: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	releaseOne.Release()

	select {
	case release := <-acquired:
		release.Release()
	case err := <-errCh:
		t.Fatalf("unexpected acquire error after releasing per-file slot: %v", err)
	case <-time.After(time.Second):
		t.Fatal("third request did not acquire per-file slot after release")
	}
}

func TestDownloadLeaseLimitsConcurrentWorkers(t *testing.T) {
	t.Parallel()

	limiter := newDownloadLimiter(2, 2)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lease, err := limiter.Acquire(ctx, "file-a")
	require.NoError(t, err)
	defer lease.Release()

	require.NoError(t, lease.AcquireWorker(ctx))
	require.NoError(t, lease.AcquireWorker(ctx))

	acquired := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		if err := lease.AcquireWorker(ctx); err != nil {
			errCh <- err
			return
		}
		acquired <- struct{}{}
	}()

	select {
	case <-acquired:
		t.Fatal("third worker should wait for a worker slot")
	case err := <-errCh:
		t.Fatalf("unexpected worker acquire error: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	lease.ReleaseWorker()

	select {
	case <-acquired:
		lease.ReleaseWorker()
	case err := <-errCh:
		t.Fatalf("unexpected worker acquire error after release: %v", err)
	case <-time.After(time.Second):
		t.Fatal("third worker did not acquire slot after release")
	}

	lease.ReleaseWorker()
}

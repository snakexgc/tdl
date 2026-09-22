package maintenance

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

type blockingRepository struct {
	entered   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
}

func (r blockingRepository) Snapshot(ctx context.Context) (ports.CleanupSnapshot, error) {
	close(r.entered)
	<-ctx.Done()
	close(r.cancelled)
	<-r.release
	return ports.CleanupSnapshot{}, ctx.Err()
}

func (blockingRepository) DeleteUnchanged(context.Context, string, []byte) (bool, error) {
	panic("must not delete after cancelled snapshot")
}

func TestStopCancelsAndDrainsCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	repo := blockingRepository{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	s := &Service{repository: repo, account: "bound", ctx: ctx, cancel: cancel}
	_, err := s.Clean(context.Background(), "other")
	require.ErrorContains(t, err, "account mismatch")
	done := make(chan error, 1)
	go func() { _, err := s.Clean(context.Background(), "bound"); done <- err }()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	stop, stopCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stopCancel()
	require.ErrorIs(t, s.Stop(stop), context.DeadlineExceeded)
	select {
	case <-repo.cancelled:
	case <-time.After(time.Second):
		t.Fatal("cleanup was not cancelled")
	}
	_, err = s.Clean(context.Background(), "bound")
	require.ErrorContains(t, err, "stopped")
	close(repo.release)
	require.ErrorIs(t, <-done, context.Canceled)
	require.NoError(t, s.Stop(context.Background()))
}

package accounttelegram

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type dialogCatalogFunc func(context.Context) ([]types.Dialog, error)

func (f dialogCatalogFunc) Dialogs(ctx context.Context) ([]types.Dialog, error) { return f(ctx) }

func TestDialogsWaitingForRefreshCanCancel(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	dialogs := NewDialogs(dialogCatalogFunc(func(context.Context) ([]types.Dialog, error) {
		calls.Add(1)
		close(entered)
		<-release
		return []types.Dialog{{}}, nil
	}))
	go func() { defer close(done); _, _ = dialogs.Dialogs(context.Background()) }()
	<-entered
	t.Cleanup(func() { close(release); <-done })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan error, 1)
	go func() { _, err := dialogs.Dialogs(ctx); waiting <- err }()
	cancel()
	select {
	case err := <-waiting:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("cancelled dialog request waited for another request's network operation")
	}
	require.EqualValues(t, 1, calls.Load())
}

func TestDialogsShareSuccessfulRefreshAndReturnCopies(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	original := []types.Dialog{{Title: "original"}}
	dialogs := NewDialogs(dialogCatalogFunc(func(context.Context) ([]types.Dialog, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return original, nil
	}))
	type result struct {
		items []types.Dialog
		err   error
	}
	const readers = 12
	results := make(chan result, readers)
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			items, err := dialogs.Dialogs(context.Background())
			results <- result{items, err}
		})
	}
	<-entered
	close(release)
	wg.Wait()
	close(results)
	for result := range results {
		require.NoError(t, result.err)
		require.Equal(t, "original", result.items[0].Title)
		result.items[0].Title = "edited"
	}
	original[0].Title = "transport edited"
	items, err := dialogs.Dialogs(context.Background())
	require.NoError(t, err)
	require.Equal(t, "original", items[0].Title)
	require.EqualValues(t, 1, calls.Load())
}

func TestDialogsCanRetryCancelledRefresh(t *testing.T) {
	entered := make(chan struct{})
	var calls atomic.Int32
	dialogs := NewDialogs(dialogCatalogFunc(func(ctx context.Context) ([]types.Dialog, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []types.Dialog{{Title: "recovered"}}, nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := dialogs.Dialogs(ctx); done <- err }()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	items, err := dialogs.Dialogs(ctx)
	require.NoError(t, err)
	require.Equal(t, "recovered", items[0].Title)
}

func TestDialogsCanRetryAfterTransportPanic(t *testing.T) {
	var calls atomic.Int32
	dialogs := NewDialogs(dialogCatalogFunc(func(context.Context) ([]types.Dialog, error) {
		if calls.Add(1) == 1 {
			panic("transport failed")
		}
		return []types.Dialog{}, nil
	}))
	require.Panics(t, func() { _, _ = dialogs.Dialogs(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := dialogs.Dialogs(ctx)
	require.NoError(t, err)
}

package bot

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/dcpool"
)

func TestBotMediaSessionRetriesSharesAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	s := &botMediaSession{ctx: ctx, cancel: cancel, serve: func(ctx context.Context, ready func(dcpool.Pool)) error {
		if calls.Add(1) == 1 {
			return errors.New("login unavailable")
		}
		close(started)
		select {
		case <-release:
			ready(nil)
		case <-ctx.Done():
			return ctx.Err()
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	defer s.close()
	_, err := s.pool(ctx)
	require.ErrorContains(t, err, "login unavailable")
	pending, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	result := make(chan error, 1)
	go func() { _, err := s.pool(pending); result <- err }()
	<-started
	cancelRequest()
	require.ErrorIs(t, <-result, context.Canceled)
	close(release)
	first, err := s.pool(ctx)
	require.NoError(t, err)
	second, err := s.pool(ctx)
	require.NoError(t, err)
	require.Same(t, first, second)
	require.EqualValues(t, 2, calls.Load())
	s.close()
	_, err = s.pool(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

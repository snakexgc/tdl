package dcpool

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

type resizeTestPool struct {
	invoke func()
	closed atomic.Int32
}

func (p *resizeTestPool) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	p.invoke()
	return nil
}
func (p *resizeTestPool) Client(context.Context, int) *tg.Client         { return tg.NewClient(p) }
func (p *resizeTestPool) Takeout(ctx context.Context, dc int) *tg.Client { return p.Client(ctx, dc) }
func (p *resizeTestPool) Default(ctx context.Context) *tg.Client         { return p.Client(ctx, 0) }
func (p *resizeTestPool) Close() error                                   { p.closed.Add(1); return nil }

func TestResizeDrainsOldRPCAndPreservesExistingClient(t *testing.T) {
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	old := &resizeTestPool{invoke: func() { close(entered); <-release }}
	var calls atomic.Int32
	next := &resizeTestPool{invoke: func() { calls.Add(1) }}
	p := newResizable(1, func(size int64) Pool {
		if size == 1 {
			return old
		}
		return next
	})
	client := p.Client(context.Background(), 2)
	go func() { defer close(finished); _ = client.Invoker().Invoke(context.Background(), nil, nil) }()
	<-entered
	p.Resize(8)
	require.Zero(t, old.closed.Load(), "in-flight request must keep its connection")
	require.NoError(t, client.Invoker().Invoke(context.Background(), nil, nil))
	require.EqualValues(t, 1, calls.Load(), "existing handles must observe the new capacity")
	close(release)
	<-finished
	require.EqualValues(t, 1, old.closed.Load())
	require.NoError(t, p.Close())
	require.NoError(t, p.Close())
	require.EqualValues(t, 1, next.closed.Load())
	require.ErrorIs(t, client.Invoker().Invoke(context.Background(), nil, nil), ErrClosed)
}

package forwardtrigger

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/eventbus"
)

func TestIntentBackpressureAccountAndShutdown(t *testing.T) {
	registry := rte.NewRegistry()
	entered, canceled, release := make(chan types.ForwardIntent, 1), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	require.NoError(t, Register(registry, func(ctx context.Context, request types.ForwardIntent) error {
		calls.Add(1)
		entered <- request
		<-ctx.Done()
		close(canceled)
		<-release
		return nil
	}, 1))
	host, err := registry.Build("alice", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	value, err := host.Resolve(ports.ForwardIntentsName)
	require.NoError(t, err)
	port := value.(ports.ForwardIntents)
	request := types.ForwardIntent{Account: "bob", MessageID: 10, Peer: types.MessagePeer{Kind: "channel", ID: 123, AccessHash: 456}}
	require.ErrorContains(t, port.Publish(context.Background(), request), "account mismatch")
	request.Account = "alice"
	require.NoError(t, port.Publish(context.Background(), request))
	select {
	case got := <-entered:
		require.Equal(t, request, got)
	case <-time.After(time.Second):
		t.Fatal("intent not delivered")
	}
	require.NoError(t, port.Publish(context.Background(), request))
	require.ErrorIs(t, port.Publish(context.Background(), request), eventbus.ErrFull)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	<-canceled
	close(release)
	require.NoError(t, host.Stop(context.Background()))
	require.Error(t, port.Publish(context.Background(), request))
	require.EqualValues(t, 1, calls.Load(), "queued work must not start during shutdown")
}

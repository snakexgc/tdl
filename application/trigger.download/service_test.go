package downloadtrigger

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

const testIntentAccount = "alice"

func TestIntentBackpressureAccountAndShutdown(t *testing.T) {
	registry := rte.NewRegistry()
	entered, canceled, release := make(chan types.DownloadIntent, 1), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	require.NoError(t, Register(registry, func(ctx context.Context, request types.DownloadIntent) error {
		calls.Add(1)
		entered <- request
		<-ctx.Done()
		close(canceled)
		<-release
		return nil
	}, 1))
	host, err := registry.Build(testIntentAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	value, err := host.Resolve(ports.DownloadIntentsName)
	require.NoError(t, err)
	port := value.(ports.DownloadIntents)
	request := types.DownloadIntent{Account: "bob", MessageID: 10, Peer: types.MessagePeer{Kind: "channel", ID: 123, AccessHash: 456}}
	require.ErrorContains(t, port.Publish(context.Background(), request), "account mismatch")
	request.Account = testIntentAccount
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

func TestRequestRepliesAndPanicDoNotStrandCallers(t *testing.T) {
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, func(context.Context, types.DownloadIntent) error { return nil }, 2,
		func(_ context.Context, request types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
			if request.MessageID == 2 {
				panic("failed source")
			}
			return types.DownloadSubmissionSummary{Link: request.Link, Queued: 3}, nil
		}))
	host, err := registry.Build(testIntentAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.DownloadRequestsName)
	require.NoError(t, err)
	port := value.(ports.DownloadRequests)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := port.Submit(ctx, types.DownloadIntent{Account: testIntentAccount, MessageID: 1, Link: "message-link"})
	require.NoError(t, err)
	require.Equal(t, 3, result.Queued)
	require.Equal(t, "message-link", result.Link)
	_, err = port.Submit(ctx, types.DownloadIntent{Account: testIntentAccount, MessageID: 2})
	require.ErrorContains(t, err, "failed source")
	result, err = port.Submit(ctx, types.DownloadIntent{Account: testIntentAccount, MessageID: 3})
	require.NoError(t, err)
	require.Equal(t, 3, result.Queued)
}

func TestRequestCancellationStopsActiveHandlerAndSkipsQueuedRequest(t *testing.T) {
	registry := rte.NewRegistry()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	require.NoError(t, Register(registry, func(context.Context, types.DownloadIntent) error { return nil }, 4,
		func(ctx context.Context, _ types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
			calls.Add(1)
			close(entered)
			<-ctx.Done()
			close(canceled)
			<-release
			return types.DownloadSubmissionSummary{}, ctx.Err()
		}))
	host, err := registry.Build(testIntentAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.DownloadRequestsName)
	require.NoError(t, err)
	port := value.(ports.DownloadRequests)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := port.Submit(ctx, types.DownloadIntent{Account: testIntentAccount, MessageID: 1})
		done <- err
	}()
	<-entered
	queuedCtx, queuedCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer queuedCancel()
	_, err = port.Submit(queuedCtx, types.DownloadIntent{Account: testIntentAccount, MessageID: 2})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach handler")
	}
	close(release)
	require.NoError(t, host.Stop(context.Background()))
	require.EqualValues(t, 1, calls.Load())
}

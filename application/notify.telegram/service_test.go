package notifytelegram

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/eventbus"
)

type testTransport struct {
	send func(context.Context, int64, string) (int, error)
	edit func(context.Context, int64, int, string) error
}

func TestRecipientTimeoutIsolation(t *testing.T) {
	for _, edit := range []bool{false, true} {
		name := "send"
		if edit {
			name = "edit"
		}
		t.Run(name, func(t *testing.T) {
			var attempted []int64
			request := func(ctx context.Context, id int64) error {
				attempted = append(attempted, id)
				if id == 1 {
					<-ctx.Done()
					return ctx.Err()
				}
				return ctx.Err()
			}
			host, service := testHost(t, &testTransport{
				send: func(ctx context.Context, id int64, _ string) (int, error) { return 42, request(ctx, id) },
				edit: func(ctx context.Context, id int64, _ int, _ string) error { return request(ctx, id) },
			})
			require.NoError(t, host.Reconfigure(context.Background(), ID, map[string]any{recipientsField: []string{"1", "2"}, "timeout_seconds": 1}))
			var err error
			if edit {
				err = service.Edit(context.Background(), []types.NotificationMessage{{Account: types.DefaultAccount, ChatID: 1, MessageID: 42}, {Account: types.DefaultAccount, ChatID: 2, MessageID: 42}}, "edited")
			} else {
				var refs []types.NotificationMessage
				refs, err = service.Send(context.Background(), "sent")
				require.Len(t, refs, 1)
				require.Equal(t, int64(2), refs[0].ChatID)
			}
			require.ErrorIs(t, err, context.DeadlineExceeded)
			require.Equal(t, []int64{1, 2}, attempted)
		})
	}
}

func TestCallerCancellationStopsFanout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var attempted []int64
	_, service := testHost(t, &testTransport{send: func(_ context.Context, id int64, _ string) (int, error) {
		attempted = append(attempted, id)
		cancel()
		return 0, context.Canceled
	}})
	_, err := service.Send(ctx, "canceled")
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, []int64{1}, attempted)
}

func (t *testTransport) Send(ctx context.Context, id int64, text string) (int, error) {
	return t.send(ctx, id, text)
}

func (t *testTransport) Edit(ctx context.Context, id int64, message int, text string) error {
	return t.edit(ctx, id, message, text)
}

func (t *testTransport) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.NotificationTransportName, t)
}
func (*testTransport) Start(context.Context) error                    { return nil }
func (*testTransport) Stop(context.Context) error                     { return nil }
func (*testTransport) Reconfigure(context.Context, config.View) error { return nil }

func testHost(t *testing.T, transport *testTransport) (*rte.Runtime, ports.Notifications) {
	t.Helper()
	r := rte.NewRegistry()
	require.NoError(t, Register(r))
	require.NoError(t, r.Register(manifest.Manifest{ID: "transport", Provides: []manifest.Port{manifest.PortOf[ports.NotificationTransport](ports.NotificationTransportName, 1, 0)}}, func() rte.Component { return transport }))
	host, err := r.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: {recipientsField: []string{"1", "2", "1", "3"}}})
	require.NoError(t, err)
	for _, status := range host.Start(context.Background()) {
		require.Equal(t, rte.Running, status.State, status.Detail)
	}
	t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
	value, err := host.Resolve(ports.NotificationsName)
	require.NoError(t, err)
	return host, value.(ports.Notifications)
}

func TestFanoutFailureDeduplicationAndConfiguration(t *testing.T) {
	var sent, edited []int64
	transport := &testTransport{
		send: func(_ context.Context, id int64, _ string) (int, error) {
			sent = append(sent, id)
			if id == 2 {
				return 0, errors.New("unreachable")
			}
			return 42, nil
		},
		edit: func(_ context.Context, id int64, _ int, _ string) error { edited = append(edited, id); return nil },
	}
	host, service := testHost(t, transport)
	ctx := context.Background()
	refs, err := service.Send(ctx, "progress")
	require.ErrorContains(t, err, "unreachable")
	require.Equal(t, []int64{1, 2, 3}, sent)
	require.Len(t, refs, 2)
	require.NoError(t, service.Edit(ctx, refs, "complete"))
	require.Equal(t, []int64{1, 3}, edited)
	require.Equal(t, types.DefaultAccount, refs[0].Account)
	refs[1].Account = "other"
	require.Error(t, service.Edit(ctx, refs, "denied"))
	require.Len(t, edited, 2, "validate every reference before sending any edits")
	require.Error(t, host.ReconfigureBatch(ctx, map[string]map[string]any{ID: {recipientsField: []string{"invalid"}}}))
	sent = nil
	_, _ = service.Send(ctx, "unchanged")
	require.Equal(t, []int64{1, 2, 3}, sent)
	require.NoError(t, host.ReconfigureBatch(ctx, map[string]map[string]any{ID: {recipientsField: []string{"3"}}}))
	sent = nil
	_, err = service.Send(ctx, "new")
	require.NoError(t, err)
	require.Equal(t, []int64{3}, sent)
}

func TestStopCancelsAndWaitsForActiveSend(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseSend := func() { releaseOnce.Do(func() { close(release) }) }
	host, service := testHost(t, &testTransport{send: func(ctx context.Context, _ int64, _ string) (int, error) {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return 0, ctx.Err()
	}})
	t.Cleanup(releaseSend)
	sent := make(chan error, 1)
	go func() { _, err := service.Send(context.Background(), "pending"); sent <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("send did not start")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- host.Stop(context.Background()) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("send was not canceled")
	}
	select {
	case <-stopped:
		t.Fatal("stop returned before active send finished")
	default:
	}
	releaseSend()
	require.ErrorIs(t, <-sent, context.Canceled)
	require.NoError(t, <-stopped)
	_, err := service.Send(context.Background(), "late")
	require.ErrorContains(t, err, "stopped")
}

func TestQueuedNotificationBackpressureAndStop(t *testing.T) {
	entered := make(chan struct{}, 1)
	exited := make(chan struct{}, 1)
	host, service := testHost(t, &testTransport{send: func(ctx context.Context, _ int64, _ string) (int, error) {
		entered <- struct{}{}
		<-ctx.Done()
		exited <- struct{}{}
		return 0, ctx.Err()
	}})
	ctx := context.Background()
	require.NoError(t, service.Enqueue(ctx, "first"))
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("event was not delivered")
	}
	for i := 0; i < 64; i++ {
		require.NoError(t, service.Enqueue(ctx, "queued"))
	}
	require.ErrorIs(t, service.Enqueue(ctx, "overflow"), eventbus.ErrFull)
	require.NoError(t, host.Stop(ctx))
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before notification transport exited")
	}
	require.Error(t, service.Enqueue(ctx, "late"))
	select {
	case <-entered:
		t.Fatal("queued notification sent during shutdown")
	default:
	}
}

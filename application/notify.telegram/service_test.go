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
)

type testTransport struct {
	send func(context.Context, int64, string) (int, error)
	edit func(context.Context, int64, int, string) error
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
	refs, err := service.Send(ctx, types.DefaultAccount, "progress")
	require.ErrorContains(t, err, "unreachable")
	require.Equal(t, []int64{1, 2, 3}, sent)
	require.Len(t, refs, 2)
	require.NoError(t, service.Edit(ctx, types.DefaultAccount, refs, "complete"))
	require.Equal(t, []int64{1, 3}, edited)
	_, err = service.Send(ctx, "other", "denied")
	require.ErrorContains(t, err, "account mismatch")
	require.Len(t, sent, 3)
	refs[1].Account = "other"
	require.Error(t, service.Edit(ctx, types.DefaultAccount, refs, "denied"))
	require.Len(t, edited, 2, "validate every reference before sending any edits")
	require.Error(t, host.ReconfigureBatch(ctx, map[string]map[string]any{ID: {recipientsField: []string{"invalid"}}}))
	sent = nil
	_, _ = service.Send(ctx, types.DefaultAccount, "unchanged")
	require.Equal(t, []int64{1, 2, 3}, sent)
	require.NoError(t, host.ReconfigureBatch(ctx, map[string]map[string]any{ID: {recipientsField: []string{"3"}}}))
	sent = nil
	_, err = service.Send(ctx, types.DefaultAccount, "new")
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
	go func() { _, err := service.Send(context.Background(), types.DefaultAccount, "pending"); sent <- err }()
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
	_, err := service.Send(context.Background(), types.DefaultAccount, "late")
	require.ErrorContains(t, err, "stopped")
}

package eventbus

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testTopic = "task.finished"

func TestTopicRoutingOwnershipAndShutdown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bus := New("a")
	defer func() { require.NoError(t, bus.Close(ctx)) }()
	reported := make(chan error, 1)
	received := make(chan Event, 1)
	_, err := bus.Subscribe(ctx, testTopic, 2, func(context.Context, Event) error { panic("failure") }, func(_ string, err error) { reported <- err })
	require.NoError(t, err)
	_, err = bus.Subscribe(ctx, testTopic, 2, func(_ context.Context, e Event) error { received <- e; return nil }, nil)
	require.NoError(t, err)
	_, err = bus.Subscribe(ctx, "other.topic", 2, func(context.Context, Event) error { t.Error("wrong topic delivered"); return nil }, nil)
	require.NoError(t, err)
	payload := json.RawMessage(`{"id":1}`)
	require.NoError(t, bus.Publish(ctx, testTopic, payload))
	payload[0] = 'x'
	select {
	case e := <-received:
		require.EqualValues(t, "a", e.Account)
		require.JSONEq(t, `{"id":1}`, string(e.Payload))
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-reported:
		require.ErrorContains(t, err, "panic")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, bus.Close(ctx))
	require.ErrorIs(t, bus.Publish(ctx, testTopic, nil), ErrClosed)
}

func TestBackpressureDoesNotPartiallyPublish(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	bus := New("a")
	entered := make(chan struct{})
	_, err := bus.Subscribe(ctx, testTopic, 1, func(ctx context.Context, _ Event) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(ctx, testTopic, 1))
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, bus.Publish(ctx, testTopic, 2))
	require.ErrorIs(t, bus.Publish(ctx, testTopic, 3), ErrFull)
	require.NoError(t, bus.Close(ctx))
}

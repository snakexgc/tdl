package dcpool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestTakeoutFallbackDoesNotDeadlockOnInitFailure(t *testing.T) {
	oldTestMode := testMode
	testMode = true
	defer func() {
		testMode = oldTestMode
	}()

	oldTakeoutInit := takeoutInit
	takeoutInit = func(context.Context, tg.Invoker) (int64, error) {
		return 0, errors.New("boom")
	}
	defer func() {
		takeoutInit = oldTakeoutInit
	}()

	pool := NewPool(telegram.NewClient(1, "hash", telegram.Options{NoUpdates: true}), 1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		client := pool.Takeout(context.Background(), 1)
		if client == nil {
			t.Error("expected fallback client, got nil")
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Takeout fallback deadlocked after takeout init failure")
	}
}

package dcpool

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

func TestCloseIsConcurrentSafeAndDoesNotReopen(t *testing.T) {
	p := NewPool(telegram.NewClient(1, "hash", telegram.Options{NoUpdates: true}), 1).(*pool)
	var closed atomic.Int32
	p.closes[1] = func() error { closed.Add(1); return errors.New("close failed") }
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := p.Close(); err == nil || err.Error() != "close failed" {
				t.Errorf("unexpected close error: %v", err)
			}
		}()
	}
	wg.Wait()
	if closed.Load() != 1 {
		t.Fatalf("closed %d times", closed.Load())
	}
	for _, client := range []*tg.Client{p.Client(context.Background(), 2), p.Takeout(context.Background(), 2), p.Default(context.Background())} {
		if err := client.Invoker().Invoke(context.Background(), nil, nil); !errors.Is(err, ErrClosed) {
			t.Fatalf("expected ErrClosed, got %v", err)
		}
	}
	if len(p.invokers) != 0 || len(p.closes) != 0 {
		t.Fatal("closed pool was reopened")
	}
}

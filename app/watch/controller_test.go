package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestControllerStopWaitsForWatchShutdown(t *testing.T) {
	oldRunWatch := runControllerWatch
	defer func() {
		runControllerWatch = oldRunWatch
	}()

	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	runControllerWatch = func(ctx context.Context, opts Options) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil
	}

	controller := NewController(context.Background(), Options{Template: "test"}, nil)
	require.True(t, controller.Start())

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("watch did not start")
	}

	stopped := make(chan struct{})
	go func() {
		controller.Stop()
		close(stopped)
	}()

	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("watch was not canceled")
	}

	select {
	case <-stopped:
		t.Fatal("stop returned before watch cleanup completed")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not wait for watch shutdown")
	}
	require.False(t, controller.Running())
}

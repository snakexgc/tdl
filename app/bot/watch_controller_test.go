package bot

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/app/watch"
)

func TestWatchControllerStopWaitsForWatchShutdown(t *testing.T) {
	oldRunWatch := runWatch
	defer func() {
		runWatch = oldRunWatch
	}()

	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	runWatch = func(ctx context.Context, opts watch.Options) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return nil
	}

	controller := newWatchController(context.Background(), watch.Options{Template: "test"}, nil)
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

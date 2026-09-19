package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	controllerTestTemplate = "test"
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

	controller := NewController(context.Background(), Options{Template: controllerTestTemplate}, nil)
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

func TestControllerReconfiguresLiveFilterWithoutRestart(t *testing.T) {
	oldRunWatch := runControllerWatch
	defer func() { runControllerWatch = oldRunWatch }()
	ready := make(chan ports.FilterRules, 1)
	runControllerWatch = func(ctx context.Context, opts Options) error {
		ready <- opts.Filter
		<-ctx.Done()
		return nil
	}
	c := NewController(context.Background(), Options{Template: controllerTestTemplate, Include: []string{testMP4Extension}}, nil)
	require.True(t, c.Start())
	t.Cleanup(c.Stop)
	var filter ports.FilterRules
	select {
	case filter = <-ready:
	case <-time.After(time.Second):
		t.Fatal("watch did not start")
	}
	ok, _ := filter.ShouldHandle(context.Background(), ports.FilterInput{Name: "clip.mp4"})
	require.True(t, ok)
	c.UpdateOptions(Options{Template: controllerTestTemplate, Include: []string{testMKVExtension}})
	ok, _ = filter.ShouldHandle(context.Background(), ports.FilterInput{Name: "clip.mp4"})
	require.False(t, ok)
	ok, _ = filter.ShouldHandle(context.Background(), ports.FilterInput{Name: "clip.mkv"})
	require.True(t, ok)
	c.UpdateOptions(Options{Template: controllerTestTemplate, FileSizeMinMB: 5, FileSizeMaxMB: 2})
	require.ErrorContains(t, c.LastError(), "minimum file size")
	ok, _ = filter.ShouldHandle(context.Background(), ports.FilterInput{Name: "clip.mkv"})
	require.True(t, ok)
	require.Zero(t, c.opts.FileSizeMinMB)
	require.True(t, c.Running())
	select {
	case <-ready:
		t.Fatal("watch was restarted")
	default:
	}
}

func TestControllerSubmitMessageLinkRequiresRunningWatcher(t *testing.T) {
	controller := NewController(context.Background(), Options{Template: controllerTestTemplate}, nil)

	_, err := controller.SubmitMessageLink(context.Background(), "https://t.me/example/1")

	require.Error(t, err)
	require.Contains(t, err.Error(), "监听下载未运行")
}

type cancelableMessageLinks struct{ entered, canceled chan struct{} }

func (cancelableMessageLinks) Validate(_ context.Context, _ types.AccountID, raw string) (string, error) {
	return raw, nil
}

func (p cancelableMessageLinks) Submit(ctx context.Context, _ types.AccountID, _ string, _ ports.MessageLinkSource, _ ports.DownloadRequests) (types.DownloadSubmissionSummary, error) {
	close(p.entered)
	<-ctx.Done()
	close(p.canceled)
	return types.DownloadSubmissionSummary{}, ctx.Err()
}

type requestCapability struct {
	ports.DownloadIntents
	ports.DownloadRequests
}

func TestControllerCancellationReachesDispatcher(t *testing.T) {
	previous := runControllerWatch
	t.Cleanup(func() { runControllerWatch = previous })
	links := cancelableMessageLinks{entered: make(chan struct{}), canceled: make(chan struct{})}
	runControllerWatch = func(ctx context.Context, opts Options) error {
		w := &Watcher{opts: opts, messageLinks: opts.messageLinks, intents: requestCapability{}}
		w.dispatcher(ctx)
		return nil
	}
	controller := NewController(context.Background(), Options{Template: controllerTestTemplate, MessageLinks: links}, nil)
	require.True(t, controller.Start())
	t.Cleanup(controller.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := controller.SubmitMessageLink(ctx, "https://t.me/example/1"); done <- err }()
	select {
	case <-links.entered:
	case <-time.After(time.Second):
		t.Fatal("message link did not reach dispatcher")
	}
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-links.canceled:
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach dispatcher")
	}
}

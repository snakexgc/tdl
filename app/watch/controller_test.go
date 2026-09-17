package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
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

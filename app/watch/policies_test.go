package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
)

const newDirectory = "new"

func TestControllerDoesNotOwnInjectedPolicyHost(t *testing.T) {
	ctx := context.Background()
	initial := Options{Template: "F", FilenameMaxLength: 255}
	host, filter, naming, err := startPolicies(ctx, "", initial)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	initial.Filter, initial.Naming = filter, naming
	old := runControllerWatch
	t.Cleanup(func() { runControllerWatch = old })
	ready := make(chan Options, 1)
	runControllerWatch = func(ctx context.Context, opts Options) error {
		ready <- opts
		<-ctx.Done()
		return nil
	}
	c := NewController(ctx, initial, nil)
	t.Cleanup(c.Stop)
	for range 2 {
		require.True(t, c.Start())
		select {
		case opts := <-ready:
			require.Same(t, naming, opts.Naming)
			require.Same(t, filter, opts.Filter)
		case <-time.After(time.Second):
			t.Fatal("watch did not start")
		}
		c.Stop()
		for _, status := range host.Statuses() {
			require.Equal(t, rte.Running, status.State)
		}
	}
}

func TestLiveNamingAndFilteringPrepareTogether(t *testing.T) {
	old := runControllerWatch
	defer func() { runControllerWatch = old }()
	ready := make(chan Options, 1)
	runControllerWatch = func(ctx context.Context, opts Options) error { ready <- opts; <-ctx.Done(); return nil }
	initial := Options{Template: "F", Dir: "old", Include: []string{testMP4Extension}, FilenameMaxLength: 255}
	c := NewController(context.Background(), initial, nil)
	require.True(t, c.Start())
	t.Cleanup(c.Stop)
	var live Options
	select {
	case live = <-ready:
	case <-time.After(time.Second):
		t.Fatal("watch did not start")
	}
	input := ports.NamingInput{BaseDir: "/downloads", Data: ports.NamingData{FileName: testVideoFile}}
	assertOld := func() {
		got, err := live.Naming.Render(context.Background(), input)
		require.NoError(t, err)
		require.Equal(t, "/downloads/old/video.mp4", got.FullPath)
		ok, _ := live.Filter.ShouldHandle(context.Background(), ports.FilterInput{Name: testVideoFile})
		require.True(t, ok)
	}
	assertOld()
	// Filter prepares first; an invalid naming template must not publish it.
	c.UpdateOptions(Options{Template: "{{", Dir: newDirectory, Include: []string{testMKVExtension}})
	require.ErrorContains(t, c.LastError(), "filename template")
	assertOld()
	// The opposite validation failure also preserves both live policies.
	c.UpdateOptions(Options{Template: "new-F", Dir: newDirectory, FileSizeMinMB: 4, FileSizeMaxMB: 2})
	require.ErrorContains(t, c.LastError(), "minimum file size")
	assertOld()
	c.UpdateOptions(Options{Template: "new-F", Dir: newDirectory, Include: []string{testMKVExtension}, FilenameMaxLength: 16})
	require.NoError(t, c.LastError())
	got, err := live.Naming.Render(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "/downloads/new/new-video.mp4", got.FullPath)
	ok, _ := live.Filter.ShouldHandle(context.Background(), ports.FilterInput{Name: testVideoFile})
	require.False(t, ok)
	require.True(t, c.Running())
	select {
	case <-ready:
		t.Fatal("policy update restarted watcher")
	default:
	}
}

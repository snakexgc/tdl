package watch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func policyWatcher(t *testing.T, opts Options) *Watcher {
	t.Helper()
	runtime, filter, naming, err := startPolicies(context.Background(), "default", opts)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, runtime.Stop(context.Background())) })
	opts.Filter = filter
	opts.Naming = naming
	return &Watcher{opts: opts}
}

func TestWatcherMatchFilterAppliesExtensionBeforeFileSizeRange(t *testing.T) {
	w := policyWatcher(t, Options{Include: []string{testMP4Extension}, FileSizeMinMB: 1, FileSizeMaxMB: 5})
	require.True(t, w.matchFilter("clip.mp4", 1024*1024))
	require.True(t, w.matchFilter("CLIP.MP4", 1024*1024))
	require.True(t, w.matchFilter("clip.mp4", 5*1024*1024))
	require.False(t, w.matchFilter("clip.mkv", 2*1024*1024))
	require.False(t, w.matchFilter("clip.mp4", 1024*1024-1))
	require.False(t, w.matchFilter("clip.mp4", 5*1024*1024+1))
}

func TestWatcherMatchFilterAppliesExcludeAndDisabledFileSizeRange(t *testing.T) {
	w := policyWatcher(t, Options{Exclude: []string{"jpg"}})
	require.True(t, w.matchFilter("archive.zip", 1<<40))
	require.False(t, w.matchFilter("photo.jpg", 10))
	require.False(t, w.matchFilter("PHOTO.JPG", 10))
}

func TestWatcherMatchFileSizeFilterSupportsUnboundedSides(t *testing.T) {
	const mb = 1024 * 1024
	maximum := policyWatcher(t, Options{FileSizeMaxMB: 5})
	require.True(t, maximum.matchFilter("a", 0))
	require.True(t, maximum.matchFilter("a", 5*mb))
	require.False(t, maximum.matchFilter("a", 5*mb+1))
	minimum := policyWatcher(t, Options{FileSizeMinMB: 5})
	require.False(t, minimum.matchFilter("a", 5*mb-1))
	require.True(t, minimum.matchFilter("a", 5*mb))
	require.True(t, minimum.matchFilter("a", 1<<40))
}

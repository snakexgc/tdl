package watch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/filterMap"
)

func TestWatcherMatchFilterAppliesExtensionBeforeFileSizeRange(t *testing.T) {
	w := &Watcher{
		include:          filterMap.New([]string{"mp4"}, addPrefixDot),
		minFileSizeBytes: fileSizeMBToBytes(1),
		maxFileSizeBytes: fileSizeMBToBytes(5),
	}

	require.True(t, w.matchFilter("clip.mp4", 1024*1024))
	require.True(t, w.matchFilter("CLIP.MP4", 1024*1024))
	require.True(t, w.matchFilter("clip.mp4", 5*1024*1024))
	require.False(t, w.matchFilter("clip.mkv", 2*1024*1024))
	require.False(t, w.matchFilter("clip.mp4", 1024*1024-1))
	require.False(t, w.matchFilter("clip.mp4", 5*1024*1024+1))
}

func TestWatcherMatchFilterAppliesExcludeAndDisabledFileSizeRange(t *testing.T) {
	w := &Watcher{
		exclude:          filterMap.New([]string{"jpg"}, addPrefixDot),
		minFileSizeBytes: fileSizeMBToBytes(0),
		maxFileSizeBytes: fileSizeMBToBytes(0),
	}

	require.True(t, w.matchFilter("archive.zip", 1<<40))
	require.False(t, w.matchFilter("photo.jpg", 10))
	require.False(t, w.matchFilter("PHOTO.JPG", 10))
}

func TestWatcherMatchFileSizeFilterSupportsUnboundedSides(t *testing.T) {
	const mb = 1024 * 1024

	maximumOnly := &Watcher{maxFileSizeBytes: fileSizeMBToBytes(5)}
	require.True(t, maximumOnly.matchFileSizeFilter(0))
	require.True(t, maximumOnly.matchFileSizeFilter(5*mb))
	require.False(t, maximumOnly.matchFileSizeFilter(5*mb+1))

	minimumOnly := &Watcher{minFileSizeBytes: fileSizeMBToBytes(5)}
	require.False(t, minimumOnly.matchFileSizeFilter(5*mb-1))
	require.True(t, minimumOnly.matchFileSizeFilter(5*mb))
	require.True(t, minimumOnly.matchFileSizeFilter(1<<40))
}

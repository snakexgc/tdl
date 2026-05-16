package watch

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/filterMap"
)

func TestWatcherMatchFilterAppliesExtensionBeforeFileSizeMB(t *testing.T) {
	w := &Watcher{
		include:          filterMap.New([]string{"mp4"}, addPrefixDot),
		minFileSizeBytes: fileSizeMBToBytes(1),
	}

	require.True(t, w.matchFilter("clip.mp4", 1024*1024))
	require.False(t, w.matchFilter("clip.mkv", 2*1024*1024))
	require.False(t, w.matchFilter("clip.mp4", 1024*1024-1))
}

func TestWatcherMatchFilterAppliesExcludeAndDisabledFileSizeMB(t *testing.T) {
	w := &Watcher{
		exclude:          filterMap.New([]string{"jpg"}, addPrefixDot),
		minFileSizeBytes: fileSizeMBToBytes(0),
	}

	require.True(t, w.matchFilter("archive.zip", 1<<40))
	require.False(t, w.matchFilter("photo.jpg", 10))
}

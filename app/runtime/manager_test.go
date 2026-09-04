package runtime

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestWatchAutoDownloadEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.Config)
		want   bool
	}{
		{name: "enabled", want: true},
		{
			name: "watch disabled",
			mutate: func(cfg *config.Config) {
				cfg.Modules.Watch = false
			},
		},
		{
			name: "aria2 module disabled",
			mutate: func(cfg *config.Config) {
				cfg.Modules.Aria2 = false
			},
		},
		{
			name: "auto download disabled",
			mutate: func(cfg *config.Config) {
				cfg.Aria2.AutoDownload = false
			},
		},
		{
			name: "internal downloader",
			mutate: func(cfg *config.Config) {
				cfg.Downloader.Mode = config.DownloaderModeInternal
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			require.Equal(t, tt.want, watchAutoDownloadEnabled(cfg))
		})
	}
}

func TestAria2AutoDownloadDoesNotReconfigureManager(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	before := effectiveAria2ManagerConfig(cfg)
	cfg.Aria2.AutoDownload = !cfg.Aria2.AutoDownload

	require.Equal(t, before, effectiveAria2ManagerConfig(cfg))
}

func TestStaleConfigTransitionIsIgnored(t *testing.T) {
	t.Parallel()

	manager := &Manager{}
	manager.applyVersion.Store(2)
	var called atomic.Bool

	require.False(t, manager.transition(1, func() { called.Store(true) }))
	require.False(t, called.Load())
	require.True(t, manager.transition(2, func() { called.Store(true) }))
	require.True(t, called.Load())
}

package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDefaultsAndAddressValidation(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	require.NoError(t, Validate(cfg))
	require.Equal(t, "desktop", cfg.Telegram.BuiltinPreset)
	require.True(t, cfg.Telegram.UseBuiltin)
	require.Equal(t, "0.0.0.0:22334", HTTPListenAddr(cfg))
	require.Equal(t, "0.0.0.0:22335", WebUIListenAddr(cfg))
	require.True(t, UsesDefaultWebUICredentials(cfg))
	cfg.HTTP.Address, cfg.HTTP.Port = "::1", 23456
	cfg.WebUI.Address, cfg.WebUI.Port = "127.0.0.1", 23457
	require.Equal(t, "[::1]:23456", HTTPListenAddr(cfg))
	require.Equal(t, "127.0.0.1:23457", WebUIListenAddr(cfg))
	cfg.HTTP.Port = 65536
	require.Error(t, Validate(cfg))
	cfg.HTTP.Port = 23456
	cfg.WebUI.Port = -1
	require.Error(t, Validate(cfg))
}

func TestRuntimeValidation(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Namespace = "user1"
	require.ErrorContains(t, Validate(cfg), "English letters only")
	cfg.Namespace = " Alice "
	cfg.Downloader.Executors = []string{DownloadExecutorLocal}
	cfg.Downloader.LocalRoot = t.TempDir()
	cfg.NTP = " time1.google.com "
	require.NoError(t, Validate(cfg))
	require.Equal(t, "Alice", cfg.Namespace)
	require.Equal(t, []string{DownloadExecutorLocal}, cfg.Downloader.Executors)
	require.Equal(t, "time1.google.com", cfg.NTP)
	for _, value := range []int{0, -1, -99} {
		cfg.PoolSize = value
		require.Equal(t, DefaultPoolSize, EffectivePoolSize(cfg))
		require.NoError(t, Validate(cfg))
		require.Equal(t, DefaultPoolSize, cfg.PoolSize)
	}
	for _, bounds := range [][2]int64{{-1, 5}, {1, -5}, {5, 2}} {
		cfg.FileSizeMinMB, cfg.FileSizeMaxMB = bounds[0], bounds[1]
		require.NoError(t, Validate(cfg))
		require.Zero(t, cfg.FileSizeMinMB)
		require.Zero(t, cfg.FileSizeMaxMB)
	}
	cfg.FileSizeMinMB, cfg.FileSizeMaxMB = 1, 5
	require.NoError(t, Validate(cfg))
	require.EqualValues(t, 1, cfg.FileSizeMinMB)
	require.EqualValues(t, 5, cfg.FileSizeMaxMB)
	_, err := NormalizeForwardMode("direct")
	require.Error(t, err)
}

func TestSharedProxyRetainsInlineCredentials(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Proxy = " http://inline:secret@127.0.0.1:8080 "
	require.Equal(t, "http://inline:secret@127.0.0.1:8080", EffectiveProxy(cfg))
	cfg.Proxy = ""
	require.Empty(t, EffectiveProxy(cfg))
}

func TestRejectsRetiredInternalDownloaderMode(t *testing.T) {
	err := validateDownloaders(DownloaderConfig{Executors: []string{"internal"}})
	require.Error(t, err)
}

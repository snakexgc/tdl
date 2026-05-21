package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestConfigSetValueUpdatesNestedFields(t *testing.T) {
	cfg := config.DefaultConfig()

	require.NoError(t, setConfigValue(cfg, "threads", "6"))
	require.NoError(t, setConfigValue(cfg, "limit", "3"))
	require.NoError(t, setConfigValue(cfg, "pool_size", "5"))
	require.NoError(t, setConfigValue(cfg, "proxy_username", "alice"))
	require.NoError(t, setConfigValue(cfg, "proxy_password", "secret"))
	require.NoError(t, setConfigValue(cfg, "http.public_base_url", "http://127.0.0.1:22334"))
	require.NoError(t, setConfigValue(cfg, "http.download_link_ttl_hours", "0"))
	require.NoError(t, setConfigValue(cfg, "include", "mp4,mkv"))
	require.NoError(t, setConfigValue(cfg, "file_size_mb", "10"))
	require.NoError(t, setConfigValue(cfg, "trigger_reactions", "👍,🔥"))
	require.NoError(t, setConfigValue(cfg, "bot.allowed_users", "1,2"))
	require.NoError(t, setConfigValue(cfg, "aria2.secret", "\"\""))
	require.NoError(t, setConfigValue(cfg, "modules.watch", "false"))
	require.NoError(t, setConfigValue(cfg, "modules.http", "false"))
	require.NoError(t, setConfigValue(cfg, "downloader.mode", "internal"))

	require.Equal(t, 6, cfg.Threads)
	require.Equal(t, 3, cfg.Limit)
	require.Equal(t, 5, cfg.PoolSize)
	require.Equal(t, "alice", cfg.ProxyUsername)
	require.Equal(t, "secret", cfg.ProxyPassword)
	require.Equal(t, "http://127.0.0.1:22334", cfg.HTTP.PublicBaseURL)
	require.Equal(t, 0, cfg.HTTP.DownloadLinkTTLHours)
	require.Equal(t, []string{"mp4", "mkv"}, cfg.Include)
	require.Equal(t, int64(10), cfg.FileSizeMB)
	require.Equal(t, []string{"👍", "🔥"}, cfg.TriggerReactions)
	require.Equal(t, []int64{1, 2}, cfg.Bot.AllowedUsers)
	require.Empty(t, cfg.Aria2.Secret)
	require.False(t, cfg.Modules.Watch)
	require.False(t, cfg.Modules.HTTP)
	require.Equal(t, config.DownloaderModeInternal, cfg.Downloader.Mode)
}

func TestConfigProtectedPathRejectsBotToken(t *testing.T) {
	require.True(t, isProtectedConfigPath("bot"))
	require.True(t, isProtectedConfigPath("bot.token"))
	require.True(t, isProtectedConfigPath("Bot.Token"))
	require.False(t, isProtectedConfigPath("bot.allowed_users"))
}

func TestConfigStoragePathIsNotConfigurable(t *testing.T) {
	cfg := config.DefaultConfig()

	_, err := getConfigValue(cfg, "storage.path")
	require.Error(t, err)
	require.Contains(t, err.Error(), "未知配置项")
}

func TestConfigurablePathsExposeDownloadConcurrencyButNotNamespace(t *testing.T) {
	require.NotContains(t, configurablePaths, "namespace")
	require.Contains(t, configurablePaths, "threads")
	require.Contains(t, configurablePaths, "limit")
}

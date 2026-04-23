package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestConfigSetValueUpdatesNestedFields(t *testing.T) {
	cfg := config.DefaultConfig()

	require.NoError(t, setConfigValue(cfg, "limit", "5"))
	require.NoError(t, setConfigValue(cfg, "http.public_base_url", "http://127.0.0.1:22334"))
	require.NoError(t, setConfigValue(cfg, "include", "mp4,mkv"))
	require.NoError(t, setConfigValue(cfg, "bot.allowed_users", "1,2"))
	require.NoError(t, setConfigValue(cfg, "aria2.secret", "\"\""))

	require.Equal(t, 5, cfg.Limit)
	require.Equal(t, "http://127.0.0.1:22334", cfg.HTTP.PublicBaseURL)
	require.Equal(t, []string{"mp4", "mkv"}, cfg.Include)
	require.Equal(t, []int64{1, 2}, cfg.Bot.AllowedUsers)
	require.Empty(t, cfg.Aria2.Secret)
}

func TestConfigProtectedPathRejectsBotToken(t *testing.T) {
	require.True(t, isProtectedConfigPath("bot"))
	require.True(t, isProtectedConfigPath("bot.token"))
	require.True(t, isProtectedConfigPath("Bot.Token"))
	require.False(t, isProtectedConfigPath("bot.allowed_users"))
}

func TestConfigGetValueReadsMapKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage["path"] = "custom"

	value, err := getConfigValue(cfg, "storage.path")
	require.NoError(t, err)
	require.Equal(t, "custom", value)
}

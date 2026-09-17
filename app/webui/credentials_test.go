package webui

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestPublicConfigRedactsTelegramHashWithoutChangingStoredConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Telegram.APIID = 12345
	cfg.Telegram.APIHash = "private-application-hash"
	public := publicConfig(cfg)
	require.Empty(t, public.Telegram.APIHash)
	require.Equal(t, cfg.Telegram.APIID, public.Telegram.APIID)
	require.NotEmpty(t, cfg.Telegram.APIHash)
	data, err := json.Marshal(public)
	require.NoError(t, err)
	require.NotContains(t, string(data), cfg.Telegram.APIHash)
	require.True(t, isBlankSensitivePatch("telegram.api_hash", json.RawMessage(`""`)))
	require.False(t, isBlankSensitivePatch("telegram.api_hash", json.RawMessage(`"replacement"`)))
}

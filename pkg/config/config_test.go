package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRuntimeDefaultsAndAddressValidation(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	require.Equal(t, "desktop", cfg.Telegram.BuiltinPreset)
	require.True(t, cfg.Telegram.UseBuiltin)
	require.Equal(t, "0.0.0.0:22334", HTTPListenAddr(cfg))
	require.Equal(t, "0.0.0.0:22335", WebUIListenAddr(cfg))
	require.True(t, UsesDefaultWebUICredentials(cfg))
	cfg.HTTP.Address, cfg.HTTP.Port = "::1", 23456
	cfg.WebUI.Address, cfg.WebUI.Port = "127.0.0.1", 23457
	require.Equal(t, "[::1]:23456", HTTPListenAddr(cfg))
	require.Equal(t, "127.0.0.1:23457", WebUIListenAddr(cfg))
}

func TestSharedProxyRetainsInlineCredentials(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.Proxy = " http://inline:secret@127.0.0.1:8080 "
	require.Equal(t, "http://inline:secret@127.0.0.1:8080", EffectiveProxy(cfg))
	cfg.Proxy = ""
	require.Empty(t, EffectiveProxy(cfg))
}

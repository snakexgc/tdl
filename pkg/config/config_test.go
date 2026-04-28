package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadMergesDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"namespace":"custom"}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Equal(t, "custom", cfg.Namespace)
	require.Equal(t, "0.0.0.0:8080", cfg.HTTP.Listen)
	require.Equal(t, "memory", cfg.HTTP.Buffer.Mode)
	require.Equal(t, 64, cfg.HTTP.Buffer.SizeMB)
	require.Equal(t, 24, cfg.HTTP.DownloadLinkTTLHours)
	require.Equal(t, "127.0.0.1:22335", cfg.WebUI.Listen)
	require.Equal(t, "admin", cfg.WebUI.Username)
	require.Empty(t, cfg.WebUI.Password)
	require.True(t, cfg.Modules.Bot)
	require.True(t, cfg.Modules.Watch)
	require.Equal(t, "http://127.0.0.1:6800/jsonrpc", cfg.Aria2.RPCURL)
	require.Equal(t, 30, cfg.Aria2.TimeoutSeconds)
	require.Empty(t, cfg.TriggerReactions)
}

func TestLoadHTTPBufferConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"http":{"buffer":{"mode":"off","size_mb":32}}}`), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)

	require.Equal(t, "off", cfg.HTTP.Buffer.Mode)
	require.Equal(t, 32, cfg.HTTP.Buffer.SizeMB)
}

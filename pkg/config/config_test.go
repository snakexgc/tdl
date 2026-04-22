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
	require.Equal(t, "http://127.0.0.1:6800/jsonrpc", cfg.Aria2.RPCURL)
	require.Equal(t, 30, cfg.Aria2.TimeoutSeconds)
}

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/consts"
)

func TestVersionDoesNotOpenOrMigrateRuntimeState(t *testing.T) {
	home := t.TempDir()
	t.Setenv(consts.EnvHome, home)
	path := filepath.Join(home, "config.json")
	invalid := []byte("invalid configuration must not affect version")
	require.NoError(t, os.WriteFile(path, invalid, 0o600))
	command := New()
	command.SetArgs([]string{versionCommand})
	require.NoError(t, command.Execute())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, invalid, data)
	entries, err := os.ReadDir(home)
	require.NoError(t, err)
	require.Len(t, entries, 1)
}

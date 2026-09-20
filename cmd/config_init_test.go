package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestConfigInitDoesNotStartDaemonOrPrintSecrets(t *testing.T) {
	home := t.TempDir()
	input := []byte(`{"bot":{"token":"private-import-token"}}`)
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.json"), input, 0o600))
	before := config.Get()
	var output bytes.Buffer
	command := New()
	command.SetOut(&output)
	command.SetArgs([]string{configInitCommand, "--home", home})
	require.NoError(t, command.Execute())
	require.Same(t, before, config.Get())
	require.NotContains(t, output.String(), "private-import-token")
	require.FileExists(t, filepath.Join(home, "tdl_config.json"))
	require.NoDirExists(t, filepath.Join(home, ".tdl"))
	require.NoDirExists(t, filepath.Join(home, "components"))
	actual, err := os.ReadFile(filepath.Join(home, "config.json"))
	require.NoError(t, err)
	require.Equal(t, input, actual)
}

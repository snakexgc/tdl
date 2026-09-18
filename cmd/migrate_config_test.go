package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
)

func TestMigrationCLIBypassesDaemonBootstrap(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "legacy.json")
	target := filepath.Join(directory, "export")
	input := []byte(`{"bot":{"token":"never-print-this"},"modules":{"bot":false}}`)
	require.NoError(t, os.WriteFile(source, input, 0o600))
	before := config.Get()
	var output bytes.Buffer
	command := New()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{migrateConfigCommand, "--source", source, "--out", target})
	require.NoError(t, command.Execute())
	require.Same(t, before, config.Get(), "preview must not initialize the daemon configuration")
	require.NotContains(t, output.String(), "never-print-this")
	_, err := os.Stat(target)
	require.True(t, os.IsNotExist(err))
	actual, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, input, actual)
	command = New()
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{migrateConfigCommand, "--source", source, "--out", target, "--write"})
	require.NoError(t, command.Execute())
	_, err = os.Stat(filepath.Join(target, "swc-downloader.aria2.json"))
	require.NoError(t, err)
}

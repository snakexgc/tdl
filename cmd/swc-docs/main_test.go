package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/configuration"
)

func TestRunCreatesOutputDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs", "configuration.md")
	require.NoError(t, run(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "download.control")
}

func TestDefaultTemplateLoadsWithoutLegacyInputs(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "tdl_config.json")
	require.NoError(t, writeDefaultConfig(path))
	_, err := configuration.Open(context.Background(), home)
	require.NoError(t, err)
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	committed, err := os.ReadFile("../../examples/tdl_config.json")
	require.NoError(t, err)
	require.Equal(t, string(committed), string(actual), "regenerate the default template with swc-docs -config-out examples/tdl_config.json")
}

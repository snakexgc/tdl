package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunCreatesOutputDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "docs", "configuration.md")
	require.NoError(t, run(path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "download.control")
}

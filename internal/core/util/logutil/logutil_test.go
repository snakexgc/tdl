package logutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestCloseReleasesLogFileForReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	logger, closeLog := NewWithClose(zap.InfoLevel, path)
	logger.Info("reset fixture")
	require.NoError(t, logger.Sync())
	require.NoError(t, closeLog())
	require.NoError(t, os.Remove(path))
}

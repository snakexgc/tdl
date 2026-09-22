package logutil

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/bsw/services/logging"
)

func TestCloseReleasesLogFileForReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest.log")
	logger, closeLog := NewWithClose(zap.InfoLevel, path)
	logger.Info("reset fixture")
	require.NoError(t, logger.Sync())
	require.NoError(t, closeLog())
	require.NoError(t, os.Remove(path))
}

func TestBothSinksRedactAndFollowLiveLevel(t *testing.T) {
	dir := t.TempDir()
	console, err := os.CreateTemp(dir, "console-*")
	require.NoError(t, err)
	previousStdout, previousStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = console, console
	t.Cleanup(func() {
		os.Stdout, os.Stderr = previousStdout, previousStderr
		require.NoError(t, console.Close())
	})
	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	logger, store, closeLog := NewSession(level, filepath.Join(dir, "latest.log"), "alice")
	defer closeLog()
	child := logger.With(zap.String("password", "private-password"))
	child.Debug("hidden")
	child.Info("Authorization: Bearer private-auth", zap.Error(errors.New("token=private-token&safe=yes")), zap.Any("metadata", map[string]string{"secret": "private-nested"}))
	level.SetLevel(zap.DebugLevel)
	logging.Slog(child).WithGroup("transport").Log(context.Background(), slog.LevelDebug, "visible", "request_id", "r1")
	require.NoError(t, logger.Sync())
	for _, name := range []string{"latest.log", "events.jsonl"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NotContains(t, string(data), "private-")
		require.NotContains(t, string(data), "hidden")
		require.Contains(t, string(data), "visible")
		require.Contains(t, string(data), "request_id")
	}
	entries, _ := store.Snapshot()
	require.Len(t, entries, 2)
	require.Contains(t, entries[0].Caller, "logutil_test.go")
	data, err := os.ReadFile(console.Name())
	require.NoError(t, err)
	require.Empty(t, string(data), "runtime logs must stay in files and WebUI, even with debug enabled")
}

func TestTextSinkFailureRemainsVisibleAfterJournalSuccess(t *testing.T) {
	dir := t.TempDir()
	// An invalid leaf prevents text writes while leaving the journal directory
	// writable. A directory at latest.log is insufficient: lumberjack rotates it.
	path := filepath.Join(dir, "latest\x00.log")
	logger, store, closeLog := NewSession(zap.InfoLevel, path, "alice")
	defer closeLog()
	logger.Info("retained despite text failure")
	entries, warning := store.Snapshot()
	require.Len(t, entries, 1)
	require.Contains(t, warning, "latest.log")
	data, err := os.ReadFile(filepath.Join(dir, "events.jsonl"))
	require.NoError(t, err)
	require.Contains(t, string(data), "retained despite text failure")
}

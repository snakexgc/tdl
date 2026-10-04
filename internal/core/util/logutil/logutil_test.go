package logutil

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/bsw/services/logging"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type fixedLogClock struct{ at time.Time }

func (c fixedLogClock) Now() time.Time          { return c.at }
func (fixedLogClock) Status() types.ClockStatus { return types.ClockStatus{Synchronized: true} }

func TestAllLogSinksFollowSharedClockAfterLateBinding(t *testing.T) {
	binding := new(rte.ClockBinding)
	dir := t.TempDir()
	logger, store, closeLog := NewSession(zap.InfoLevel, filepath.Join(dir, "latest.log"), "alice", binding)
	t.Cleanup(func() { require.NoError(t, closeLog()) })
	child := logger.Named("clock").With(zap.String("component", "time.sync"))
	child.Info("before calibration")
	entries, _ := store.Snapshot()
	require.WithinDuration(t, time.Now(), entries[0].At, time.Second)
	first := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	binding.Bind(fixedLogClock{first})
	child.Info("zap calibrated")
	logging.Slog(child, binding).WithGroup("transport").With("request", "clock").Info("slog calibrated")
	require.NoError(t, store.Write(logging.Entry{Message: "implicit timestamp"}))
	entries, _ = store.Snapshot()
	require.Len(t, entries, 4)
	for _, entry := range entries[:3] {
		require.Equal(t, first, entry.At)
	}
	data, err := os.ReadFile(filepath.Join(dir, "latest.log"))
	require.NoError(t, err)
	require.Contains(t, string(data), first.In(time.Local).Format("2006-01-02 15:04:05"))
	binding.Bind(nil)
	child.Info("system fallback")
	entries, _ = store.Snapshot()
	require.WithinDuration(t, time.Now(), entries[0].At, time.Second)
}

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

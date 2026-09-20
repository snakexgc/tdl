package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestJournalRedactionRestoreAndLevels(t *testing.T) {
	var output bytes.Buffer
	store := New(&output)
	level := zap.NewAtomicLevelAt(zap.InfoLevel)
	logger := zap.New(NewCore(store, level)).With(zap.String("account", "alice"), zap.String("component", "downloader.local"))
	logger.Debug("hidden")
	logger.Info("download", zap.String("password", "private-password"), zap.Any("metadata", map[string]string{"api_hash": "private-hash", "value": "safe"}), zap.String("url", "socks5://someone:private-proxy@localhost:1080"))
	level.SetLevel(zap.DebugLevel)
	Slog(logger).With("component", "console.bot").Error("error https://api.telegram.org/bot123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef/test", "error", errors.New("secret=private-secret"))
	logger.Debug("visible")
	records, _ := store.Snapshot()
	require.Len(t, records, 3)
	require.Equal(t, "visible", records[0].Message)
	require.Equal(t, "console.bot", records[1].Component)
	require.Equal(t, "alice", records[1].Account)
	for _, value := range []string{"private-password", "private-hash", "private-proxy", "private-secret", "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"} {
		require.NotContains(t, output.String(), value)
	}
	require.Contains(t, output.String(), "safe")
	path := filepath.Join(t.TempDir(), "events.jsonl")
	require.NoError(t, os.WriteFile(path, append(output.Bytes(), []byte("broken tail")...), 0o600))
	restored := New(nil)
	require.NoError(t, restored.Restore(path))
	previous, _ := restored.Snapshot()
	require.Equal(t, records, previous)
	require.NoError(t, restored.Write(Entry{Message: "after restart"}))
	next, _ := restored.Snapshot()
	require.Greater(t, next[0].ID, records[0].ID)
}

func TestConcurrentLogsStayBoundedAndIndependent(t *testing.T) {
	store := New(nil)
	logger := Slog(zap.New(NewCore(store, zap.DebugLevel)))
	group := logger.WithGroup("transport").With("ready", true).WithGroup("details")
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for count := range 700 {
				group.Log(context.Background(), slog.LevelInfo, fmt.Sprintf("%d/%d", worker, count), "sequence", count)
			}
		}()
	}
	workers.Wait()
	records, _ := store.Snapshot()
	require.Len(t, records, Capacity)
	require.Equal(t, uint64(5600), records[0].ID)
	require.Equal(t, uint64(601), records[len(records)-1].ID)
	for index := 1; index < len(records); index++ {
		require.Less(t, records[index].ID, records[index-1].ID)
	}
	records[0].Message = "mutated"
	unchanged, _ := store.Snapshot()
	require.NotEqual(t, "mutated", unchanged[0].Message)
	var details map[string]any
	require.NoError(t, json.Unmarshal([]byte(unchanged[0].Details), &details))
	transport := details["transport"].(map[string]any)
	require.Equal(t, true, transport["ready"])
	require.Contains(t, transport["details"], "sequence")
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }
func TestDiskFailureKeepsLiveLogs(t *testing.T) {
	store := New(brokenWriter{})
	require.Error(t, store.Write(Entry{Message: strings.Repeat("中", 10000)}))
	entries, warning := store.Snapshot()
	require.Len(t, entries, 1)
	require.Less(t, len(entries[0].Message), 8200)
	require.Contains(t, warning, "disk full")
}

func TestSinkWarningsRecoverIndependently(t *testing.T) {
	var journal bytes.Buffer
	store := New(&journal)
	store.RecordSinkError("latest.log", errors.New("text unavailable"))
	store.RecordSinkError("stderr", errors.New("console unavailable"))
	require.NoError(t, store.Write(Entry{Message: "journal succeeds"}))
	_, warning := store.Snapshot()
	require.Contains(t, warning, "text unavailable")
	require.Contains(t, warning, "console unavailable")
	store.RecordSinkError("latest.log", nil)
	_, warning = store.Snapshot()
	require.NotContains(t, warning, "text unavailable")
	require.Contains(t, warning, "console unavailable")
	store.RecordSinkError("stderr", nil)
	_, warning = store.Snapshot()
	require.Empty(t, warning)
}

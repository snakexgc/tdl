package logging

import (
	"context"
	"log/slog"
	"runtime"
	"testing"
	"testing/slogtest"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestSlogHandlerContract(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	err := slogtest.TestHandler(Slog(zap.New(core)).Handler(), func() []map[string]any {
		result := []map[string]any{}
		for _, entry := range observed.All() {
			item := entry.ContextMap()
			item[slog.MessageKey] = entry.Message
			item[slog.LevelKey] = entry.Level
			if !entry.Time.IsZero() {
				item[slog.TimeKey] = entry.Time
			}
			result = append(result, item)
		}
		return result
	})
	require.NoError(t, err)
}

func TestSlogLevelsCallerAndTime(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	logger := Slog(zap.New(core, zap.AddCaller()))
	for _, level := range []slog.Level{-100, -1, 0, 3, 4, 7, 8, 12, 16, 100} {
		logger.Log(context.Background(), level, "custom level")
	}
	want := []zapcore.Level{zap.DebugLevel, zap.DebugLevel, zap.InfoLevel, zap.InfoLevel, zap.WarnLevel, zap.WarnLevel, zap.ErrorLevel, zap.ErrorLevel, zap.ErrorLevel, zap.ErrorLevel}
	for i, entry := range observed.All() {
		require.Equal(t, want[i], entry.Level)
		require.Contains(t, entry.Caller.File, "slog_test.go")
	}
	pc, _, _, _ := runtime.Caller(0)
	at := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	record := slog.NewRecord(at, slog.LevelInfo, "timestamp", pc)
	require.NoError(t, logger.Handler().Handle(context.Background(), record))
	require.Equal(t, at, observed.All()[len(want)].Time)
}

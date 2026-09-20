package bot

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/snakexgc/tdl/bsw/services/logging"
)

func TestShutdownAwareTelegoLoggerSuppressesTransientGetUpdatesNoise(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	logger := newShutdownAwareTelegoLogger("secret-token")
	logger.logger = logging.Slog(zap.New(core))

	logger.Errorf("Execution error getUpdates: request call: %s", `http do request: Post "https://api.telegram.org/botsecret-token/getUpdates": EOF`)
	logger.Errorf("Getting updates: telego: getUpdates: internal execution: %s", `request call: http do request: Post "https://api.telegram.org/botsecret-token/getUpdates": EOF`)
	logger.Errorf("Retrying getting updates in 8s...")
	require.Empty(t, observed.All())

	logger.Errorf("Execution error getUpdates: %s", "invalid bot token")
	require.Len(t, observed.All(), 1)
	require.Contains(t, observed.All()[0].Message, "invalid bot token")
	require.NotContains(t, observed.All()[0].Message, "secret-token")
}

func TestShutdownAwareTelegoLoggerFiltersOnlyShutdownNoise(t *testing.T) {
	core, observed := observer.New(zap.InfoLevel)
	logger := newShutdownAwareTelegoLogger("secret-token")
	logger.logger = logging.Slog(zap.New(core))

	logger.Errorf("Getting updates: telego: getUpdates: request call: %s", "interrupt signal received")
	require.Contains(t, observed.All()[0].Message, "interrupt signal received")

	observed.TakeAll()
	logger.SetShuttingDown()
	logger.Errorf("Execution error getUpdates: request call: %s", "interrupt signal received")
	logger.Errorf("Getting updates: telego: getUpdates: %s", "context canceled")
	logger.Errorf("Retrying getting updates in 8s...")
	require.Empty(t, observed.All())

	logger.Errorf("Execution error sendMessage: Post %q failed", "https://api.telegram.org/botsecret-token/sendMessage")
	require.Len(t, observed.All(), 1)
	require.Contains(t, observed.All()[0].Message, "sendMessage")
	require.Contains(t, observed.All()[0].Message, telegoTokenReplacement)
	require.NotContains(t, observed.All()[0].Message, "secret-token")
}

func TestTelegoRecoverableErrorsRemainAvailableAtDebug(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	previous := slog.Default()
	slog.SetDefault(logging.Slog(zap.New(core)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	logger := newShutdownAwareTelegoLogger("secret-token")
	logger.Errorf("Getting updates: Post https://api.telegram.org/botsecret-token/getUpdates: EOF")
	logger.Errorf("Retrying getting updates in 8s...")
	logger.Debugf("request body: private-body")
	require.Len(t, observed.All(), 2)
	for _, entry := range observed.All() {
		require.Equal(t, zap.DebugLevel, entry.Level)
		require.Equal(t, "console.bot", entry.ContextMap()["component"])
		require.NotContains(t, entry.Message, "secret-token")
	}
}

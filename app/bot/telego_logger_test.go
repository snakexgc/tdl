package bot

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShutdownAwareTelegoLoggerFiltersOnlyShutdownNoise(t *testing.T) {
	var out bytes.Buffer
	logger := newShutdownAwareTelegoLogger("secret-token")
	logger.out = &out

	logger.Errorf("Getting updates: telego: getUpdates: request call: %s", "interrupt signal received")
	require.Contains(t, out.String(), "interrupt signal received")

	out.Reset()
	logger.SetShuttingDown()
	logger.Errorf("Execution error getUpdates: request call: %s", "interrupt signal received")
	logger.Errorf("Getting updates: telego: getUpdates: %s", "context canceled")
	logger.Errorf("Retrying getting updates in 8s...")
	require.Empty(t, out.String())

	logger.Errorf("Execution error sendMessage: Post %q failed", "https://api.telegram.org/botsecret-token/sendMessage")
	require.Contains(t, out.String(), "sendMessage")
	require.Contains(t, out.String(), telegoTokenReplacement)
	require.NotContains(t, out.String(), "secret-token")
}

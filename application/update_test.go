package application

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStandaloneUpdaterAssemblesSharedNetworkConfiguration(t *testing.T) {
	// The CLI composition must work without starting Telegram or the WebUI.
	service, stop, err := updatePort(context.Background(), "socks5://127.0.0.1:1080")
	require.NoError(t, err)
	t.Cleanup(stop)
	require.NotNil(t, service)
}

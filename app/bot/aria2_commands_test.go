package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAria2WebSocketURL(t *testing.T) {
	got, err := aria2WebSocketURL("http://127.0.0.1:6800/jsonrpc")
	require.NoError(t, err)
	require.Equal(t, "ws://127.0.0.1:6800/jsonrpc", got)

	got, err = aria2WebSocketURL("https://aria2.example/jsonrpc")
	require.NoError(t, err)
	require.Equal(t, "wss://aria2.example/jsonrpc", got)

	got, err = aria2WebSocketURL("127.0.0.1:6800/jsonrpc")
	require.NoError(t, err)
	require.Equal(t, "ws://127.0.0.1:6800/jsonrpc", got)
}

package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestAriaNgURL(t *testing.T) {
	require.Equal(t,
		"http://ariang.js.org/#!/settings/rpc/set/ws/example.com/6800/jsonrpc/c2VjcmV0",
		ariaNgURL(config.Aria2Config{
			RPCURL: "http://example.com:6800/jsonrpc",
			Secret: "secret",
		}),
	)

	require.Equal(t,
		"http://ariang.js.org/#!/settings/rpc/set/wss/example.com/443/rpc",
		ariaNgURL(config.Aria2Config{
			RPCURL: "https://example.com/rpc",
		}),
	)
}

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

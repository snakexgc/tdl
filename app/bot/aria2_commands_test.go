package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/pkg/config"
)

func TestExtractAria2Links(t *testing.T) {
	links := extractAria2Links("hello\nhttps://example.com/a.zip\nmagnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=a\nhttps://example.com/a.zip")

	require.Equal(t, []string{
		"https://example.com/a.zip",
		"magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=a",
	}, links)
}

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

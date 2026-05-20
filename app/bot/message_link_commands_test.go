package bot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExtractHTTPLinks(t *testing.T) {
	links := extractHTTPLinks("hello\nhttps://t.me/example/1, https://example.com/a.zip https://t.me/example/1")

	require.Equal(t, []string{
		"https://t.me/example/1",
		"https://example.com/a.zip",
	}, links)
}

func TestContainsMagnetLink(t *testing.T) {
	require.True(t, containsMagnetLink("magnet:?xt=urn:btih:0123456789abcdef"))
	require.False(t, containsMagnetLink("https://t.me/example/1"))
}

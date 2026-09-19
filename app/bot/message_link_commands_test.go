package bot

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/watch"
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

func TestMessageLinkSummaryPreservesPartialAcceptanceAndUncertainty(t *testing.T) {
	text := formatMessageLinkSubmissionResult([]watch.MessageLinkSubmissionResult{{Link: "https://t.me/example/1", Total: 5, Queued: 2, Skipped: 1, Failed: 1, Uncertain: 1}}, []string{"RPC response lost"})
	require.Contains(t, text, "已接受：2")
	require.Contains(t, text, "跳过：1")
	require.Contains(t, text, "未提交：1")
	require.Contains(t, text, "结果待确认：1")
	require.Contains(t, text, "RPC response lost")
}

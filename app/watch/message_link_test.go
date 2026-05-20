package watch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateTelegramMessageHTTPLinkAcceptsSupportedFormats(t *testing.T) {
	cases := []string{
		"https://t.me/example/123",
		"http://telegram.me/example/456?comment=789",
		"https://t.me/c/1697797156/151",
		"https://t.me/example/45662/55005",
		"https://t.me/c/1492447836/251015/251021",
	}

	for _, tc := range cases {
		got, err := ValidateTelegramMessageHTTPLink(tc)
		require.NoError(t, err)
		require.Equal(t, tc, got)
	}
}

func TestValidateTelegramMessageHTTPLinkRejectsNonTelegramLinks(t *testing.T) {
	_, err := ValidateTelegramMessageHTTPLink("https://example.com/file.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "域名")

	_, err = ValidateTelegramMessageHTTPLink("https://t.me/example")
	require.Error(t, err)
	require.Contains(t, err.Error(), "路径")

	_, err = ValidateTelegramMessageHTTPLink("https://t.me/s/example/123")
	require.Error(t, err)

	_, err = ValidateTelegramMessageHTTPLink("tg://openmessage?user_id=1&message_id=2")
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP/HTTPS")
}

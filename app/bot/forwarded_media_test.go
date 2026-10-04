package bot

import (
	"testing"

	"github.com/mymmrac/telego"
	"github.com/stretchr/testify/require"
)

func TestForwardedMediaUsesForwardedFileAndOrigin(t *testing.T) {
	message := &telego.Message{
		ForwardOrigin: &telego.MessageOriginHiddenUser{Date: 123, SenderUserName: "原发送者"},
		Caption:       "说明", MediaGroupID: "album-1",
		Document: &telego.Document{FileID: "document-id", FileName: "资料.pdf", FileSize: 42, MimeType: "application/pdf"},
	}
	media, ok := forwardedMedia(message)
	require.True(t, ok)
	require.Equal(t, "document-id", media.FileID)
	require.Equal(t, "资料.pdf", media.FileName)
	require.Equal(t, int64(42), media.FileSize)
	require.Equal(t, int64(123), media.Date)
	require.Equal(t, "原发送者", media.PeerName)
	require.Equal(t, "album-1", media.AlbumID)
	require.Equal(t, "说明", media.Caption)

	message.ForwardOrigin = nil
	_, ok = forwardedMedia(message)
	require.False(t, ok, "ordinary uploads must not start a forwarded-message download")
}

func TestForwardedMediaChoosesMediaInsteadOfThumbnail(t *testing.T) {
	message := &telego.Message{
		ForwardOrigin: &telego.MessageOriginChannel{Date: 456, Chat: telego.Chat{Title: "频道"}},
		Animation:     &telego.Animation{FileID: "animation-id", FileSize: 12},
		Document:      &telego.Document{FileID: "duplicate-document-id", FileSize: 12},
	}
	media, ok := forwardedMedia(message)
	require.True(t, ok)
	require.Equal(t, "animation-id", media.FileID)
	require.Equal(t, "频道", media.PeerName)

	message.Animation, message.Document = nil, nil
	message.Photo = []telego.PhotoSize{{FileID: "small", FileSize: 2}, {FileID: "large", FileSize: 8}}
	media, ok = forwardedMedia(message)
	require.True(t, ok)
	require.Equal(t, "large", media.FileID)
	require.Equal(t, int64(8), media.FileSize)
}

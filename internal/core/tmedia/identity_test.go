package tmedia

import (
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
)

func TestVideoClassificationUsesAttributesMIMEAndFileName(t *testing.T) {
	for _, doc := range []*tg.Document{
		{Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{}}},
		{MimeType: "video/webm"},
		{MimeType: "application/octet-stream", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "CLIP.MKV"}}},
	} {
		require.True(t, IsVideo(&tg.Message{Media: &tg.MessageMediaDocument{Document: doc}}))
	}
	require.False(t, IsVideo(&tg.Message{Media: &tg.MessageMediaDocument{Document: &tg.Document{MimeType: "image/png"}}}))
	require.False(t, IsVideo(&tg.Message{Media: &tg.MessageMediaPhoto{}}))
}

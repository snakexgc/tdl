package tmedia

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gotd/td/tg"
)

func Identity(media *Media) string {
	if media == nil {
		return ""
	}
	var kind, thumb string
	var id int64
	switch location := media.InputFileLoc.(type) {
	case *tg.InputDocumentFileLocation:
		kind, id, thumb = "document", location.ID, location.ThumbSize
	case *tg.InputPhotoFileLocation:
		kind, id, thumb = "photo", location.ID, location.ThumbSize
	default:
		return ""
	}
	if thumb != "" {
		thumb = "_" + strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(thumb)
	}
	return fmt.Sprintf("%s_%d%s", kind, id, thumb)
}

func IsVideo(message *tg.Message) bool {
	if message == nil {
		return false
	}
	media, ok := message.Media.(*tg.MessageMediaDocument)
	if !ok {
		return false
	}
	document, ok := media.Document.(*tg.Document)
	if !ok {
		return false
	}
	for _, attribute := range document.Attributes {
		if _, ok := attribute.(*tg.DocumentAttributeVideo); ok {
			return true
		}
	}
	if strings.HasPrefix(strings.ToLower(document.MimeType), "video/") {
		return true
	}
	switch strings.ToLower(filepath.Ext(GetDocumentName(document))) {
	case ".mp4", ".mkv", ".mov", ".webm", ".avi", ".m4v", ".mpeg", ".mpg", ".ts", ".mts", ".m2ts", ".flv", ".wmv", ".3gp", ".ogv":
		return true
	default:
		return false
	}
}

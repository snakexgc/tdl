package watch

import (
	"context"
	"io"
	"path/filepath"

	"github.com/gabriel-vasile/mimetype"
	"github.com/go-faster/errors"
	gotddownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/fsutil"
	"github.com/iyear/tdl/core/util/tutil"
)

// downloadFile downloads a media file using gotd/td's built-in downloader
// with real-time progress tracking via progressWriteAt.
// threads controls the number of parallel download connections for this file.
func downloadFile(ctx context.Context, client *tg.Client, media *tmedia.Media, to io.WriterAt, threads int) error {
	_, err := gotddownloader.NewDownloader().
		WithPartSize(1024 * 1024). // MaxPartSize
		Download(client, media.InputFileLoc).
		WithThreads(tutil.BestThreads(media.Size, threads)).
		Parallel(ctx, to)
	if err != nil {
		return errors.Wrap(err, "parallel download")
	}
	return nil
}

// rewriteExt detects the actual MIME type from file content and adjusts the extension.
func (w *Watcher) rewriteExt(filePath, currentName string) string {
	mime, err := mimetype.DetectFile(filePath)
	if err != nil {
		return currentName
	}
	ext := mime.Extension()
	if ext != "" && filepath.Ext(currentName) != ext {
		return fsutil.GetNameWithoutExt(currentName) + ext
	}
	return currentName
}

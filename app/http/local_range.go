package httpdl

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

// Completed local files remain downloadable while Telegram is disconnected.
// Bind the opened file to this request so HEAD, ranges and streaming all refer
// to the same verified file, even if its path is replaced during the request.
type localFileTransfer struct {
	rangeTransfer
	file      *os.File
	closeOnce sync.Once
	closeErr  error
}

func (p *downloadProxy) openLocalTransfer(ctx context.Context, task *downloadTask) (*localFileTransfer, error) {
	record, found, err := taskhub.NewLocalRepository(p.tasks.kv).Get(ctx, task.ID)
	if err != nil {
		return nil, err
	}
	if !found || record.TaskID != task.ID || record.Status != types.LocalDownloadStatusComplete ||
		record.Total != task.FileSize || record.Completed != task.FileSize || !filepath.IsAbs(record.Path) {
		return nil, nil
	}
	file, err := os.Open(record.Path)
	if err != nil {
		// Missing or inaccessible local files can still be read from Telegram.
		return nil, nil
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != task.FileSize {
		_ = file.Close()
		return nil, nil
	}
	return &localFileTransfer{rangeTransfer: rangeTransfer{proxy: p, task: task}, file: file}, nil
}

func (t *localFileTransfer) Ready(ctx context.Context) error { return ctx.Err() }

func (t *localFileTransfer) Acquire(ctx context.Context) (ports.DownloadLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Local disk reads do not consume Telegram connection or file slots.
	return t, nil
}

func (t *localFileTransfer) Stream(ctx context.Context, _ ports.DownloadLease, start, end int64, w io.Writer) error {
	length := end - start + 1
	reader := localRangeReader{ctx: ctx, reader: io.NewSectionReader(t.file, start, length)}
	_, err := io.CopyN(w, reader, length)
	return err
}

func (t *localFileTransfer) Release() { _ = t.Close() }

func (t *localFileTransfer) Close() error {
	t.closeOnce.Do(func() { t.closeErr = t.file.Close() })
	return t.closeErr
}

type localRangeReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r localRangeReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

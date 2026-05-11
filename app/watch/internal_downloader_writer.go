package watch

import (
	"context"
	"io"
	"time"
)

type internalProgressWriter struct {
	ctx       context.Context
	store     *internalTaskStore
	id        string
	total     int64
	completed int64
	lastSave  time.Time
	w         io.Writer
}

func (w *internalProgressWriter) Write(p []byte) (int, error) {
	if err := w.checkStatus(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	if n > 0 {
		w.completed += int64(n)
		if time.Since(w.lastSave) >= time.Second || w.completed >= w.total {
			w.flush()
		}
	}
	return n, err
}

func (w *internalProgressWriter) flush() {
	record, ok, err := w.store.Get(context.WithoutCancel(w.ctx), w.id)
	if err != nil || !ok {
		return
	}
	record.Completed = w.completed
	if record.Total <= 0 {
		record.Total = w.total
	}
	_ = w.store.Save(context.WithoutCancel(w.ctx), record)
	w.lastSave = time.Now()
}

func (w *internalProgressWriter) checkStatus() error {
	record, ok, err := w.store.Get(w.ctx, w.id)
	if err != nil {
		return err
	}
	if !ok {
		return errInternalDownloadRemoved
	}
	switch record.Status {
	case InternalDownloadStatusPaused:
		return errInternalDownloadPaused
	case InternalDownloadStatusRemoved:
		return errInternalDownloadRemoved
	default:
		return nil
	}
}

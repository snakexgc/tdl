package local

import (
	"context"
	"io"
	"time"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type internalProgressWriter struct {
	record    types.LocalDownloadRecord
	ctx       context.Context
	store     ports.LocalDownloadRepository
	id        string
	total     int64
	completed int64
	lastSave  time.Time
	w         io.Writer
	speed     *speedCalc
}

func (w *internalProgressWriter) Write(p []byte) (int, error) {
	if err := w.checkStatus(); err != nil {
		return 0, err
	}
	n, err := w.w.Write(p)
	if n > 0 {
		w.completed += int64(n)
		w.speed.update(int64(n))
		if time.Since(w.lastSave) >= time.Second || w.completed >= w.total {
			w.flush()
		}
	}
	return n, err
}

func (w *internalProgressWriter) flush() {
	_, _ = w.store.Update(context.WithoutCancel(w.ctx), w.id, func(record *types.LocalDownloadRecord) bool {
		if record.Status != types.InternalDownloadStatusActive || !sameExecution(*record, w.record) {
			return false
		}
		record.Completed = w.completed
		if record.Total <= 0 {
			record.Total = w.total
		}
		record.DownloadSpeed = w.speed.speed()
		return true
	})
	w.lastSave = time.Now()
}

func (w *internalProgressWriter) checkStatus() error {
	record, ok, err := w.store.Get(w.ctx, w.id)
	if err != nil {
		return err
	}
	if !ok || !sameExecution(record, w.record) {
		return types.ErrLocalDownloadRemoved
	}
	switch record.Status {
	case types.InternalDownloadStatusPaused:
		return types.ErrLocalDownloadPaused
	case types.InternalDownloadStatusRemoved:
		return types.ErrLocalDownloadRemoved
	default:
		return nil
	}
}

package watch

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func (w *Watcher) matchFilter(name string, size int64) bool {
	if w.opts.Filter == nil {
		return true
	}
	ok, _ := w.opts.Filter.ShouldHandle(context.Background(), ports.FilterInput{Account: w.opts.Account, Name: name, Size: size})
	return ok
}

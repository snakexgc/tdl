package watch

import (
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

func newMemoryTaskStorage() *storage.Memory { return &storage.Memory{} }

func newTestWatchRuntime(cfg *config.Config, opts Options, store storage.Storage, logger *zap.Logger) *watchRuntime {
	if opts.HTTPService == nil {
		opts.HTTPService = httpdl.NewService(cfg, store, logger)
	}
	return newWatchRuntime(opts, store, logger)
}

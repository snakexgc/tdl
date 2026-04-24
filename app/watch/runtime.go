package watch

import (
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

type watchRuntime struct {
	proxy            *downloadProxy
	aria2            *aria2Client
	aria2Tasks       *aria2TaskStore
	pools            *poolHolder
	outputRoot       string
	ensureOutputDirs bool
}

func newWatchRuntime(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	pools := &poolHolder{}

	return &watchRuntime{
		proxy:      newDownloadProxy(cfg.HTTP, cfg.Limit, cfg.Threads, pools, kvd, logger),
		aria2:      newAria2Client(cfg.Aria2),
		aria2Tasks: newAria2TaskStore(kvd),
		pools:      pools,
	}
}

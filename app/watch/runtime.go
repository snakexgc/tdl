package watch

import (
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

type watchRuntime struct {
	proxy *downloadProxy
	aria2 *aria2Client
	pools *poolHolder
}

func newWatchRuntime(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	pools := &poolHolder{}

	return &watchRuntime{
		proxy: newDownloadProxy(cfg.HTTP, pools, kvd, logger),
		aria2: newAria2Client(cfg.Aria2),
		pools: pools,
	}
}

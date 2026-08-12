package watch

import (
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type watchRuntime struct {
	proxy            *httpdl.Proxy
	internal         *internalDownloader
	pools            *httpdl.PoolHolder
	outputRoot       string
	ensureOutputDirs bool
}

func newWatchRuntime(cfg *config.Config, opts Options, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	service := opts.HTTPService
	if service == nil {
		service = httpdl.NewService(cfg, kvd, logger)
	}
	proxy := service.Proxy()
	pools := service.Pools()
	runtime := &watchRuntime{
		proxy: proxy,
		pools: pools,
	}
	runtime.internal = newInternalDownloader(proxy, kvd, logger, cfg)
	return runtime
}

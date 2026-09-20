package watch

import (
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type watchRuntime struct {
	local  ports.DownloadExecutor
	proxy  *httpdl.Proxy
	worker *local.Worker
	pools  *httpdl.PoolHolder
}

func newWatchRuntime(opts Options, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	service := opts.HTTPService
	proxy := service.Proxy()
	pools := service.Pools()
	runtime := &watchRuntime{
		proxy: proxy,
		pools: pools,
	}
	runtime.worker = local.New(localSource{proxy: proxy, scheduler: proxy.Scheduler()}, taskhub.NewLocalRepository(kvd), logger)
	return runtime
}

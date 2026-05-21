package watch

import (
	"go.uber.org/zap"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/app/watch/aria2"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

type watchRuntime struct {
	proxy            *httpdl.Proxy
	aria2            *aria2.Client
	aria2Tasks       *aria2.TaskStore
	internal         *internalDownloader
	pools            *httpdl.PoolHolder
	outputRoot       string
	ensureOutputDirs bool
}

func newWatchRuntime(cfg *config.Config, opts Options, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	pools := &httpdl.PoolHolder{}
	limit := effectiveWatchOptionLimit(opts.Limit, cfg)
	threads := effectiveWatchOptionThreads(opts.Threads, cfg)

	proxy := httpdl.NewProxy(cfg.HTTP, limit, threads, pools, kvd, logger)
	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		proxy.SetTaskTTL(0)
	}
	runtime := &watchRuntime{
		proxy:      proxy,
		aria2:      aria2.NewClient(cfg.Aria2),
		aria2Tasks: aria2.NewTaskStore(kvd, httpdl.LinkTTL(cfg.HTTP)),
		pools:      pools,
	}
	runtime.internal = newInternalDownloader(proxy, kvd, logger, cfg)
	return runtime
}

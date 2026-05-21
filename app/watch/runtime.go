package watch

import (
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/watch/aria2"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

type watchRuntime struct {
	proxy            *downloadProxy
	aria2            *aria2.Client
	aria2Tasks       *aria2.TaskStore
	internal         *internalDownloader
	pools            *poolHolder
	outputRoot       string
	ensureOutputDirs bool
}

func newWatchRuntime(cfg *config.Config, opts Options, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	pools := &poolHolder{}
	limit := effectiveWatchOptionLimit(opts.Limit, cfg)
	threads := effectiveWatchOptionThreads(opts.Threads, cfg)

	proxy := newDownloadProxy(cfg.HTTP, limit, threads, pools, kvd, logger)
	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		proxy.tasks.ttl = 0
	}
	runtime := &watchRuntime{
		proxy:      proxy,
		aria2:      aria2.NewClient(cfg.Aria2),
		aria2Tasks: aria2.NewTaskStore(kvd, downloadLinkTTL(cfg.HTTP)),
		pools:      pools,
	}
	runtime.internal = newInternalDownloader(proxy, kvd, logger, cfg)
	return runtime
}

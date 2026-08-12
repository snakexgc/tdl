package watch

import (
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/app/aria2"
	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type watchRuntime struct {
	proxy                *httpdl.Proxy
	aria2                *aria2.Client
	aria2Tasks           *aria2.TaskStore
	telegramErrRegulator *aria2.TelegramErrorRegulator
	zeroSpeedMonitor     *aria2.ZeroSpeedMonitor
	internal             *internalDownloader
	pools                *httpdl.PoolHolder
	outputRoot           string
	ensureOutputDirs     bool
}

func newWatchRuntime(cfg *config.Config, opts Options, kvd storage.Storage, logger *zap.Logger) *watchRuntime {
	service := opts.HTTPService
	if service == nil {
		service = httpdl.NewService(cfg, kvd, logger)
	}
	proxy := service.Proxy()
	pools := service.Pools()
	runtime := &watchRuntime{
		proxy:      proxy,
		aria2:      aria2.NewClient(cfg.Aria2),
		aria2Tasks: aria2.NewTaskStore(kvd, httpdl.LinkTTL(cfg.HTTP)),
		pools:      pools,
	}
	runtime.internal = newInternalDownloader(proxy, kvd, logger, cfg)
	return runtime
}

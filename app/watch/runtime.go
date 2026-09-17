package watch

import (
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

type watchRuntime struct {
	local            ports.DownloadExecutor
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
	account := opts.Account
	if account == "" {
		account = types.DefaultAccount
	}
	runtime.local = &localExecutor{account: account, worker: runtime.internal}
	return runtime
}

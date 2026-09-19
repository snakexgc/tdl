package watch

import (
	"context"
	"fmt"
	"io"

	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	local "github.com/snakexgc/tdl/application/downloader.local"
	transfer "github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

// Compatibility adapter; execution and recovery are owned by the component.
type internalDownloader struct {
	proxy     *httpdl.Proxy
	store     *internalTaskStore
	scheduler *transfer.Scheduler
	logger    *zap.Logger
	worker    *local.Worker
}

func newInternalDownloader(proxy *httpdl.Proxy, kvd storage.Storage, logger *zap.Logger, cfg *config.Config) *internalDownloader {
	d := &internalDownloader{proxy: proxy, store: newInternalTaskStore(kvd), scheduler: internalDownloadLimiter(proxy, cfg), logger: logger}
	d.worker = d.component()
	return d
}
func effectiveDownloadLimit(cfg *config.Config) int { return config.EffectiveLimit(cfg) }
func internalDownloadLimiter(proxy *httpdl.Proxy, cfg *config.Config) *transfer.Scheduler {
	if proxy != nil && proxy.Scheduler() != nil {
		return proxy.Scheduler()
	}
	if cfg == nil {
		cfg = config.Get()
	}
	return transfer.NewScheduler(effectiveDownloadLimit(cfg), config.EffectivePoolSize(cfg))
}

func (d *internalDownloader) component() *local.Worker {
	if d.worker == nil {
		d.worker = local.New(localSource{proxy: d.proxy, scheduler: d.scheduler}, d.store, d.logger)
	}
	return d.worker
}
func (d *internalDownloader) Start(ctx context.Context) error { return d.component().Start(ctx) }
func (d *internalDownloader) Stop()                           { d.component().Stop() }
func (d *internalDownloader) PauseForShutdown(ctx context.Context) ([]string, error) {
	return d.component().PauseForShutdown(ctx)
}

func (d *internalDownloader) requeueInterrupted(ctx context.Context) error {
	return d.component().Recover(ctx)
}
func (d *internalDownloader) runTask(ctx context.Context, id string) { d.component().Execute(ctx, id) }
func (d *internalDownloader) Add(ctx context.Context, task *httpdl.Task, target types.DownloadSubmission) (InternalDownloadInfo, error) {
	return d.component().Add(ctx, localSourceInfo(task), target)
}

type localSource struct {
	proxy     *httpdl.Proxy
	scheduler *transfer.Scheduler
}

func localSourceInfo(task *httpdl.Task) types.LocalDownloadSource {
	if task == nil {
		return types.LocalDownloadSource{}
	}
	info := types.LocalDownloadSource{ID: task.ID, FileName: task.FileName, FileSize: task.FileSize, Available: task.Media != nil}
	if task.Media != nil {
		info.DC = task.Media.DC
	}
	return info
}

func (s localSource) Get(ctx context.Context, id string) (types.LocalDownloadSource, bool, error) {
	if s.proxy == nil {
		return types.LocalDownloadSource{}, false, fmt.Errorf("download source is unavailable")
	}
	task, ok, err := s.proxy.Tasks().Get(ctx, id)
	return localSourceInfo(task), ok, err
}

func (s localSource) Acquire(ctx context.Context, id string, dc int) (ports.DownloadLease, error) {
	if s.scheduler == nil {
		return nil, fmt.Errorf("download limiter is unavailable")
	}
	return s.scheduler.Acquire(ctx, id, dc)
}

func (s localSource) Stream(ctx context.Context, id string, lease ports.DownloadLease, start, end int64, w io.Writer) error {
	task, ok, err := s.proxy.Tasks().Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("download source disappeared")
	}
	concrete, ok := lease.(*transfer.TaskLease)
	if !ok {
		return fmt.Errorf("invalid download lease")
	}
	return s.proxy.StreamParallel(ctx, task, concrete, start, end, w)
}

package watch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/watch/transfer"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

const (
	internalDownloadQueueSize            = 100
	internalDownloadPollInterval         = 5 * time.Second
	internalDownloadShutdownPauseTimeout = 5 * time.Second
)

type internalDownloader struct {
	proxy  *downloadProxy
	store  *internalTaskStore
	limit  *transfer.Limiter
	logger *zap.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
	queue   chan string
	queued  map[string]struct{}
	active  map[string]struct{}
}

func newInternalDownloader(proxy *downloadProxy, kvd storage.Storage, logger *zap.Logger, cfg *config.Config) *internalDownloader {
	if logger == nil {
		logger = zap.NewNop()
	}
	limit := internalDownloadLimiter(proxy, cfg)
	return &internalDownloader{
		proxy:  proxy,
		store:  newInternalTaskStore(kvd),
		limit:  limit,
		logger: logger.Named("internal-downloader"),
		queued: map[string]struct{}{},
		active: map[string]struct{}{},
	}
}

func effectiveDownloadThreads(cfg *config.Config) int {
	return config.EffectiveThreads(cfg)
}

func effectiveDownloadLimit(cfg *config.Config) int {
	return config.EffectiveLimit(cfg)
}

func internalDownloadLimiter(proxy *downloadProxy, cfg *config.Config) *transfer.Limiter {
	if proxy != nil && proxy.limiter != nil {
		return proxy.limiter
	}
	if cfg == nil {
		cfg = config.Get()
	}
	var httpCfg config.HTTPConfig
	if cfg != nil {
		httpCfg = cfg.HTTP
	}
	return transfer.NewLimiter(effectiveDownloadLimit(cfg), effectiveDownloadThreads(cfg), httpMemoryBufferSlots(httpCfg.Buffer))
}

func (d *internalDownloader) Start(ctx context.Context) error {
	if d == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	d.running = true
	d.cancel = cancel
	d.done = done
	d.queue = make(chan string, internalDownloadQueueSize)
	d.queued = map[string]struct{}{}
	d.active = map[string]struct{}{}
	d.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			d.mu.Lock()
			d.running = false
			d.cancel = nil
			d.done = nil
			d.queue = nil
			d.queued = map[string]struct{}{}
			d.active = map[string]struct{}{}
			d.mu.Unlock()
		}()
		d.loop(runCtx)
	}()

	if err := d.requeueInterrupted(ctx); err != nil {
		d.Stop()
		return err
	}
	if err := d.enqueuePending(ctx); err != nil {
		d.Stop()
		return err
	}
	return nil
}

func (d *internalDownloader) Stop() {
	if d == nil {
		return
	}
	d.mu.Lock()
	cancel := d.cancel
	done := d.done
	d.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(controllerStopTimeout):
	}
}

func (d *internalDownloader) PauseForShutdown(ctx context.Context) ([]string, error) {
	if d == nil || d.store == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), internalDownloadShutdownPauseTimeout)
	defer cancel()

	records, err := d.store.Records(shutdownCtx)
	if err != nil {
		return nil, err
	}
	paused := make([]string, 0, len(records))
	for _, record := range records {
		if !shouldPauseInternalDownloadForShutdown(record.Status) {
			continue
		}
		record.Status = InternalDownloadStatusPaused
		record.Error = ""
		if err := d.store.Save(shutdownCtx, record); err != nil {
			return paused, err
		}
		paused = append(paused, record.ID)
	}
	return paused, nil
}

func (d *internalDownloader) Add(ctx context.Context, task *downloadTask, prepared preparedFileTask) (InternalDownloadInfo, error) {
	if d == nil || task == nil {
		return InternalDownloadInfo{}, errors.New("internal downloader is not initialized")
	}
	record := internalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       prepared.dir,
		Out:       prepared.out,
		Path:      prepared.fullPath,
		Total:     task.FileSize,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}
	if existing, ok, err := d.store.Get(ctx, record.ID); err != nil {
		return InternalDownloadInfo{}, err
	} else if ok && !existing.CreatedAt.IsZero() {
		record.CreatedAt = existing.CreatedAt
	}
	if err := d.store.Save(ctx, record); err != nil {
		return InternalDownloadInfo{}, err
	}
	if !d.queueID(record.ID) {
		return InternalDownloadInfo{}, errors.New("internal download queue is full")
	}
	return internalDownloadInfo(record), nil
}

func (d *internalDownloader) loop(ctx context.Context) {
	ticker := time.NewTicker(internalDownloadPollInterval)
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.enqueuePending(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Warn("Failed to enqueue pending internal downloads", zap.Error(err))
			}
		case id := <-d.queue:
			if !d.markRunning(id) {
				continue
			}
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				defer d.markStopped(id)
				d.runTask(ctx, id)
			}(id)
		}
	}
}

func (d *internalDownloader) enqueuePending(ctx context.Context) error {
	records, err := d.store.Records(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if shouldRunInternalDownload(record.Status) {
			d.queueID(record.ID)
		}
	}
	return nil
}

func (d *internalDownloader) requeueInterrupted(ctx context.Context) error {
	records, err := d.store.Records(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Status != InternalDownloadStatusActive {
			continue
		}
		record.Status = InternalDownloadStatusQueued
		record.Error = ""
		if err := d.store.Save(ctx, record); err != nil {
			return err
		}
	}
	return nil
}

func (d *internalDownloader) queueID(id string) bool {
	d.mu.Lock()
	if !d.running || d.queue == nil {
		d.mu.Unlock()
		return true
	}
	if _, ok := d.queued[id]; ok {
		d.mu.Unlock()
		return true
	}
	if _, ok := d.active[id]; ok {
		d.mu.Unlock()
		return true
	}
	d.queued[id] = struct{}{}
	queue := d.queue
	d.mu.Unlock()

	select {
	case queue <- id:
		return true
	default:
		d.mu.Lock()
		delete(d.queued, id)
		d.mu.Unlock()
		return false
	}
}

func (d *internalDownloader) markRunning(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.queued, id)
	if id == "" {
		return false
	}
	if _, ok := d.active[id]; ok {
		return false
	}
	d.active[id] = struct{}{}
	return true
}

func (d *internalDownloader) markStopped(id string) {
	d.mu.Lock()
	delete(d.active, id)
	d.mu.Unlock()
}

func (d *internalDownloader) runTask(ctx context.Context, id string) {
	record, ok, err := d.store.Get(ctx, id)
	if err != nil {
		d.logger.Warn("Failed to load internal download", zap.String("id", id), zap.Error(err))
		return
	}
	if !ok || !shouldRunInternalDownload(record.Status) {
		return
	}

	if d.proxy == nil || d.proxy.tasks == nil {
		d.markError(ctx, record, errors.New("download proxy is not initialized"))
		return
	}
	task, ok, err := d.proxy.tasks.Get(ctx, record.TaskID)
	if err != nil {
		d.markError(ctx, record, err)
		return
	}
	if !ok {
		d.markError(ctx, record, errors.New("download link record not found"))
		return
	}
	if task.FileSize > 0 {
		record.Total = task.FileSize
	}
	if record.Total <= 0 {
		d.markError(ctx, record, errors.New("invalid file size"))
		return
	}
	if record.Path == "" {
		record.Path = joinTargetPath(record.Dir, record.Out)
	}
	if record.Dir == "" {
		record.Dir = filepath.Dir(record.Path)
	}
	if err := os.MkdirAll(record.Dir, 0o755); err != nil {
		d.markError(ctx, record, errors.Wrap(err, "create target directory"))
		return
	}

	completed, err := prepareInternalPartialFile(record.Path, record.Total)
	if err != nil {
		d.markError(ctx, record, err)
		return
	}
	record.Completed = completed
	if completed >= record.Total {
		d.markComplete(ctx, record)
		return
	}

	if d.limit == nil {
		d.markError(ctx, record, errors.New("download limiter is not initialized"))
		return
	}
	lease, err := d.limit.Acquire(ctx, record.ID)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		d.markError(ctx, record, err)
		return
	}
	defer lease.Release()

	latest, ok, err := d.store.Get(ctx, record.ID)
	if err != nil {
		d.markError(ctx, record, err)
		return
	}
	if !ok || !shouldRunInternalDownload(latest.Status) {
		return
	}

	record.Status = InternalDownloadStatusActive
	record.Error = ""
	if err := d.store.Save(ctx, record); err != nil {
		d.logger.Warn("Failed to persist internal download status", zap.String("id", record.ID), zap.Error(err))
	}

	file, err := os.OpenFile(record.Path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		d.markError(ctx, record, errors.Wrap(err, "open target file"))
		return
	}
	if _, err := file.Seek(completed, io.SeekStart); err != nil {
		_ = file.Close()
		d.markError(ctx, record, errors.Wrap(err, "seek target file"))
		return
	}

	writer := &internalProgressWriter{
		ctx:       ctx,
		store:     d.store,
		id:        record.ID,
		total:     record.Total,
		completed: completed,
		w:         file,
	}
	stream := d.proxy.stream
	if stream == nil {
		stream = d.proxy.streamTask
	}
	err = stream(ctx, task, lease, completed, record.Total-1, writer)
	closeErr := file.Close()
	if closeErr != nil && err == nil {
		err = closeErr
	}
	if err == nil {
		writer.flush()
		record.Completed = record.Total
		d.markComplete(ctx, record)
		return
	}

	current, ok, loadErr := d.store.Get(context.WithoutCancel(ctx), record.ID)
	if loadErr == nil && ok {
		record = current
	}
	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, errInternalDownloadPaused):
		record.Status = InternalDownloadStatusPaused
		record.Error = ""
		_ = d.store.Save(context.WithoutCancel(ctx), record)
	case errors.Is(err, errInternalDownloadRemoved):
		return
	default:
		d.markError(context.WithoutCancel(ctx), record, err)
	}
}

func (d *internalDownloader) markComplete(ctx context.Context, record internalDownloadRecord) {
	record.Status = InternalDownloadStatusComplete
	record.Completed = record.Total
	record.Error = ""
	if err := d.store.Save(context.WithoutCancel(ctx), record); err != nil {
		d.logger.Warn("Failed to mark internal download complete", zap.String("id", record.ID), zap.Error(err))
	}
	markPersistentDownloadTaskDownloaded(context.WithoutCancel(ctx), d.store.kv, record.TaskID)
	d.logger.Info("Internal download completed",
		zap.String("id", record.ID),
		zap.String("path", record.Path))
}

func (d *internalDownloader) markError(ctx context.Context, record internalDownloadRecord, err error) {
	record.Status = InternalDownloadStatusError
	record.Error = err.Error()
	if saveErr := d.store.Save(context.WithoutCancel(ctx), record); saveErr != nil {
		d.logger.Warn("Failed to mark internal download error",
			zap.String("id", record.ID),
			zap.Error(saveErr))
	}
	d.logger.Warn("Internal download failed",
		zap.String("id", record.ID),
		zap.String("path", record.Path),
		zap.Error(err))
}

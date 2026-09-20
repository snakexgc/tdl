package local

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/targetpath"
)

const (
	internalDownloadQueueSize            = 100
	internalDownloadPollInterval         = 5 * time.Second
	internalDownloadShutdownPauseTimeout = 5 * time.Second
)

type Worker struct {
	pollInterval    atomic.Int64
	shutdownTimeout atomic.Int64
	configChanged   chan struct{}
	source          ports.LocalDownloadSource
	store           ports.LocalDownloadRepository
	logger          *zap.Logger

	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	done    chan struct{}
	queue   chan string
	queued  map[string]struct{}
	active  map[string]struct{}
}

func New(source ports.LocalDownloadSource, store ports.LocalDownloadRepository, logger *zap.Logger) *Worker {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Worker{source: source, store: store, logger: logger.Named("local-downloader").With(zap.String("component", "downloader.local")), queued: map[string]struct{}{}, active: map[string]struct{}{}, configChanged: make(chan struct{}, 1)}
}

func (d *Worker) Start(ctx context.Context) error {
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

	if err := d.Recover(ctx); err != nil {
		d.Stop()
		return err
	}
	if err := d.enqueuePending(ctx); err != nil {
		d.Stop()
		return err
	}
	return nil
}

func (d *Worker) Stop() {
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
	<-done
}

func (d *Worker) PauseForShutdown(ctx context.Context) ([]string, error) {
	if d == nil || d.store == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), d.pauseTimeout())
	defer cancel()

	records, err := d.store.Records(shutdownCtx)
	if err != nil {
		return nil, err
	}
	paused := make([]string, 0, len(records))
	for _, record := range records {
		changed, err := d.store.Update(shutdownCtx, record.ID, func(current *types.LocalDownloadRecord) bool {
			if !shouldPauseInternalDownloadForShutdown(current.Status) {
				return false
			}
			current.Status = types.InternalDownloadStatusPaused
			current.Error = ""
			current.DownloadSpeed = 0
			return true
		})
		if err != nil {
			return paused, err
		}
		if changed {
			paused = append(paused, record.ID)
		}
	}

	return paused, nil
}

func (d *Worker) Add(ctx context.Context, task types.LocalDownloadSource, prepared types.DownloadSubmission) (types.InternalDownloadInfo, error) {
	if d == nil || task.ID == "" {
		return types.InternalDownloadInfo{}, errors.New("internal downloader is not initialized")
	}
	record := types.LocalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       prepared.Dir,
		Out:       prepared.Out,
		Path:      prepared.FullPath,
		Total:     task.FileSize,
		Status:    types.InternalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}
	var err error
	record, err = d.store.Create(ctx, record)
	if err != nil {
		return types.InternalDownloadInfo{}, err
	}
	// The durable record is accepted even when the in-memory wake-up queue is
	// full; the periodic scanner will pick it up without duplicate submission.
	d.queueID(record.ID)

	return internalDownloadInfo(record), nil
}

func (d *Worker) loop(ctx context.Context) {
	ticker := time.NewTicker(d.scanInterval())
	defer ticker.Stop()
	var wg sync.WaitGroup
	defer wg.Wait()

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.configChanged:
			ticker.Reset(d.scanInterval())
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
				d.Execute(ctx, id)
			}(id)
		}
	}
}

func (d *Worker) enqueuePending(ctx context.Context) error {
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

func (d *Worker) Recover(ctx context.Context) error {
	records, err := d.store.Records(ctx)
	if err != nil {
		return err
	}
	for _, record := range records {
		_, err := d.store.Update(ctx, record.ID, func(current *types.LocalDownloadRecord) bool {
			if current.Status != types.InternalDownloadStatusActive {
				return false
			}
			current.Status = types.InternalDownloadStatusQueued
			current.Error = ""
			return true
		})
		if err != nil {
			return err
		}
	}

	return nil
}

func (d *Worker) queueID(id string) bool {
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

func (d *Worker) markRunning(id string) bool {
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

func (d *Worker) markStopped(id string) {
	d.mu.Lock()
	delete(d.active, id)
	d.mu.Unlock()
}

func (d *Worker) Execute(ctx context.Context, id string) {
	record, ok, err := d.store.Get(ctx, id)
	if err != nil {
		d.logger.Warn("Failed to load internal download", zap.String("id", id), zap.Error(err))
		return
	}
	if !ok || !shouldRunInternalDownload(record.Status) {
		return
	}

	if d.source == nil {
		d.markError(ctx, record, errors.New("download proxy is not initialized"))
		return
	}
	task, ok, err := d.source.Get(ctx, record.TaskID)
	if err != nil {
		d.markError(ctx, record, err)
		return
	}
	if !ok {
		d.markError(ctx, record, errors.New("download link record not found"))
		return
	}
	if !task.Available {
		d.markError(ctx, record, errors.New("download media is not available"))
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
		record.Path = targetpath.JoinTargetPath(record.Dir, record.Out)
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

	if d.source == nil {
		d.markError(ctx, record, errors.New("download limiter is not initialized"))
		return
	}
	lease, err := d.source.Acquire(ctx, record.ID, task.DC)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return
		}
		d.markError(ctx, record, err)
		return
	}
	defer lease.Release()

	now := time.Now()
	claimed, err := d.store.Update(ctx, record.ID, func(current *types.LocalDownloadRecord) bool {
		if !sameExecution(*current, record) || !shouldRunInternalDownload(current.Status) {
			return false
		}
		current.Status = types.InternalDownloadStatusActive
		current.Error = ""
		current.StartedAt = &now
		current.DownloadSpeed = 0
		current.Total = record.Total
		current.Completed = completed
		record = *current
		return true
	})
	if err != nil {
		level := zap.ErrorLevel
		if errors.Is(err, context.Canceled) {
			level = zap.DebugLevel
		}
		d.logger.Log(level, "Failed to claim local download", zap.String("task_id", record.TaskID), zap.String("id", record.ID), zap.Error(err))
		return
	}
	if !claimed {
		return
	}
	d.logger.Info("本地下载已开始", zap.String("id", record.ID), zap.String("task_id", record.TaskID), zap.Int64("total", record.Total), zap.Int64("completed", completed))

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
		record:    record,
		ctx:       ctx,
		store:     d.store,
		id:        record.ID,
		total:     record.Total,
		completed: completed,
		w:         file,
		speed:     &speedCalc{},
	}
	err = d.source.Stream(ctx, task.ID, lease, completed, record.Total-1, writer)
	closeErr := file.Close()
	if closeErr != nil && err == nil {
		err = closeErr
	}
	if err == nil && writer.completed != record.Total {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		writer.flush()
		record.Completed = record.Total
		d.markComplete(ctx, record)
		return
	}

	switch {
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, types.ErrLocalDownloadPaused):
		return
	case errors.Is(err, types.ErrLocalDownloadRemoved):
		return
	default:
		d.markError(context.WithoutCancel(ctx), record, err)
	}
}

func (d *Worker) markComplete(ctx context.Context, record types.LocalDownloadRecord) {
	changed, err := d.store.Update(context.WithoutCancel(ctx), record.ID, func(current *types.LocalDownloadRecord) bool {
		if !sameExecution(*current, record) {
			return false
		}
		if current.Status != types.InternalDownloadStatusActive && !shouldRunInternalDownload(current.Status) {
			return false
		}
		current.Status = types.InternalDownloadStatusComplete
		current.Completed = record.Total
		current.Total = record.Total
		current.Error = ""
		current.DownloadSpeed = 0
		return true
	})
	if err != nil {
		d.logger.Warn("Failed to mark local download complete", zap.String("id", record.ID), zap.Error(err))
		return
	}
	if !changed {
		return
	}
	if err := d.store.MarkDownloaded(context.WithoutCancel(ctx), record.TaskID); err != nil {
		d.logger.Warn("Failed to mark source downloaded", zap.String("id", record.ID), zap.Error(err))
	}
	d.logger.Info("本地下载已完成", zap.String("id", record.ID), zap.String("task_id", record.TaskID), zap.Int64("bytes", record.Total))
}

func (d *Worker) markError(ctx context.Context, record types.LocalDownloadRecord, cause error) {
	changed, err := d.store.Update(context.WithoutCancel(ctx), record.ID, func(current *types.LocalDownloadRecord) bool {
		if !sameExecution(*current, record) {
			return false
		}
		if current.Status != types.InternalDownloadStatusActive && !shouldRunInternalDownload(current.Status) {
			return false
		}
		current.Status = types.InternalDownloadStatusError
		current.Error = cause.Error()
		current.DownloadSpeed = 0
		return true
	})
	if err != nil {
		d.logger.Warn("Failed to persist local download error", zap.String("id", record.ID), zap.Error(err))
	}
	if changed || err != nil {
		d.logger.Error("Local download failed", zap.String("id", record.ID), zap.String("task_id", record.TaskID), zap.Error(cause))
	}
}

// Persisted timestamps distinguish a recreated task and a restarted attempt
// without changing the legacy record format.
func sameExecution(current, previous types.LocalDownloadRecord) bool {
	if !current.CreatedAt.Equal(previous.CreatedAt) {
		return false
	}
	if current.StartedAt == nil || previous.StartedAt == nil {
		return current.StartedAt == nil && previous.StartedAt == nil
	}
	return current.StartedAt.Equal(*previous.StartedAt)
}

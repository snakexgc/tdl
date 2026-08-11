package httpdl

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/snakexgc/tdl/app/http/transfer"
	"github.com/snakexgc/tdl/core/dcpool"
	"github.com/snakexgc/tdl/core/logctx"
	"github.com/snakexgc/tdl/core/tmedia"
)

type poolHolder struct {
	mu   sync.RWMutex
	pool dcpool.Pool
}

func (h *poolHolder) Set(pool dcpool.Pool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.pool = pool
}

func (h *poolHolder) Get() dcpool.Pool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.pool
}

type taskStreamer func(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer) error

// sourceRegistry retains only media metadata and refresh synchronization. It
// deliberately never stores downloaded file bytes.
type sourceRegistry struct {
	mu      sync.Mutex
	sources map[string]*sourceEntry
}

type sourceEntry struct {
	source   *telegramMediaSource
	refs     int
	lastUsed time.Time
}

type sourceHandle struct {
	registry *sourceRegistry
	taskID   string
	entry    *sourceEntry
	once     sync.Once
}

func newSourceRegistry() *sourceRegistry {
	return &sourceRegistry{
		sources: make(map[string]*sourceEntry),
	}
}

func (r *sourceRegistry) Acquire(task *downloadTask, refresh func(context.Context) (*tmedia.Media, error)) *sourceHandle {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.sources[task.ID]
	if entry == nil {
		entry = &sourceEntry{source: &telegramMediaSource{media: task.Media, refresh: refresh}}
		r.sources[task.ID] = entry
	} else {
		// The shared source may already contain a freshly renewed file reference.
		// A request that loaded the task before that renewal must not overwrite it
		// with stale metadata; only its refresh callback needs replacing.
		entry.source.UpdateRefresh(refresh)
	}
	entry.refs++
	entry.lastUsed = time.Now()
	return &sourceHandle{registry: r, taskID: task.ID, entry: entry}
}

func (h *sourceHandle) Source() *telegramMediaSource {
	if h == nil || h.entry == nil {
		return nil
	}
	return h.entry.source
}

func (h *sourceHandle) Release() {
	if h == nil || h.registry == nil || h.entry == nil {
		return
	}
	h.once.Do(func() {
		h.registry.mu.Lock()
		if current := h.registry.sources[h.taskID]; current == h.entry {
			if current.refs > 0 {
				current.refs--
			}
			current.lastUsed = time.Now()
		}
		h.registry.mu.Unlock()
	})
}

func (r *sourceRegistry) CleanupIdle(now time.Time, ttl time.Duration) int {
	if r == nil || ttl <= 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	released := 0
	for taskID, entry := range r.sources {
		if entry.refs == 0 && now.Sub(entry.lastUsed) >= ttl {
			delete(r.sources, taskID)
			released++
		}
	}
	return released
}

type downloadChunkJob struct {
	index int
	req   telegramChunkRequest
	skip  int
	take  int
}

type downloadChunkResult struct {
	index   int
	data    []byte
	release func()
}

func buildDownloadChunkJobs(start, end int64) []downloadChunkJob {
	if end < start {
		return nil
	}
	remaining := end - start + 1
	next := start
	index := 0
	jobs := make([]downloadChunkJob, 0, int((remaining+int64(telegramGetFileFragmentWindowSize)-1)/int64(telegramGetFileFragmentWindowSize)))
	for next <= end {
		fragmentStart := (next / int64(telegramGetFileFragmentWindowSize)) * int64(telegramGetFileFragmentWindowSize)
		fragmentEnd := fragmentStart + int64(telegramGetFileFragmentWindowSize) - 1
		needStart := next
		needEnd := min(end, fragmentEnd)
		reqOffset := alignDown(needStart, telegramGetFilePreciseAlignment)
		reqEnd := alignUp(needEnd+1, telegramGetFilePreciseAlignment)
		fragmentLimit := fragmentStart + int64(telegramGetFileFragmentWindowSize)
		if reqEnd > fragmentLimit {
			reqEnd = fragmentLimit
		}
		jobs = append(jobs, downloadChunkJob{
			index: index,
			req:   telegramChunkRequest{offset: reqOffset, limit: int(reqEnd - reqOffset)},
			skip:  int(needStart - reqOffset),
			take:  int(needEnd - needStart + 1),
		})
		next = needEnd + 1
		index++
	}
	return jobs
}

// streamTelegramMedia serves one HTTP request sequentially. Parallelism is
// created by independent Range requests, all governed by the shared lease.
func streamTelegramMedia(ctx context.Context, pool dcpool.Pool, source *telegramMediaSource, lease *transfer.TaskLease, reporter TelegramFileErrorReporter, start, end int64, w io.Writer) error {
	if end < start {
		return errors.New("invalid byte range")
	}
	jobs := buildDownloadChunkJobs(start, end)
	if len(jobs) == 0 {
		return nil
	}
	logger := logctx.From(ctx)
	var written int64
	for _, job := range jobs {
		chunkLease, err := lease.AcquireChunk(ctx)
		if err != nil {
			return err
		}
		raw, err := source.FetchChunk(ctx, pool, reporter, job.req)
		if err != nil {
			chunkLease.Release()
			return err
		}
		data, err := sliceTelegramChunk(raw, job.skip, job.take)
		if err != nil {
			chunkLease.Release()
			return errors.Wrap(err, "slice telegram file chunk")
		}
		n, err := writeFull(w, data)
		written += int64(n)
		if err == nil {
			err = flushWriter(w)
		}
		chunkLease.Release()
		if err != nil {
			logger.Error("Writing HTTP response body failed",
				zap.Int("chunk_size", len(data)),
				zap.Int("written", n),
				zap.Int64("bytes_written", written),
				zap.Error(err))
			return errors.Wrap(err, "write http response")
		}
	}
	return nil
}

// streamTelegramMediaParallel preserves parallel internal downloads without
// introducing a retained cache. Chunk leases stay held until ordered output is
// written, so decoded chunk memory remains bounded by the DC capacity.
func streamTelegramMediaParallel(ctx context.Context, pool dcpool.Pool, source *telegramMediaSource, lease *transfer.TaskLease, reporter TelegramFileErrorReporter, start, end int64, w io.Writer) error {
	if end < start {
		return errors.New("invalid byte range")
	}
	jobs := buildDownloadChunkJobs(start, end)
	if len(jobs) == 0 {
		return nil
	}
	workers := min(lease.Capacity(), len(jobs))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobsCh := make(chan downloadChunkJob)
	results := make(chan downloadChunkResult)
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		defer close(jobsCh)
		for _, job := range jobs {
			select {
			case <-gctx.Done():
				return gctx.Err()
			case jobsCh <- job:
			}
		}
		return nil
	})
	for range workers {
		g.Go(func() error {
			for job := range jobsCh {
				chunkLease, err := lease.AcquireChunk(gctx)
				if err != nil {
					return err
				}
				raw, err := source.FetchChunk(gctx, pool, reporter, job.req)
				if err != nil {
					chunkLease.Release()
					return err
				}
				data, err := sliceTelegramChunk(raw, job.skip, job.take)
				if err != nil {
					chunkLease.Release()
					return errors.Wrap(err, "slice telegram file chunk")
				}
				result := downloadChunkResult{index: job.index, data: data, release: chunkLease.Release}
				select {
				case <-gctx.Done():
					releaseDownloadChunk(result)
					return gctx.Err()
				case results <- result:
				}
			}
			return nil
		})
	}
	done := make(chan error, 1)
	go func() {
		done <- g.Wait()
		close(results)
	}()

	pending := make(map[int]downloadChunkResult, workers)
	next := 0
	for result := range results {
		pending[result.index] = result
		for {
			chunk, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			_, err := writeFull(w, chunk.data)
			releaseDownloadChunk(chunk)
			if err != nil {
				cancel()
				releasePendingDownloadChunks(pending)
				_ = waitAndReleaseDownloadResults(done, results)
				return errors.Wrap(err, "write download output")
			}
			next++
		}
	}
	err := <-done
	if err != nil {
		releasePendingDownloadChunks(pending)
		return err
	}
	if next != len(jobs) {
		releasePendingDownloadChunks(pending)
		return io.ErrUnexpectedEOF
	}
	return nil
}

func writeFull(w io.Writer, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		n, err := w.Write(data[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func flushWriter(w io.Writer) error {
	if flusher, ok := w.(interface{ FlushError() error }); ok {
		return flusher.FlushError()
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func releaseDownloadChunk(result downloadChunkResult) {
	if result.release != nil {
		result.release()
	}
}

func releasePendingDownloadChunks(pending map[int]downloadChunkResult) {
	for index, result := range pending {
		releaseDownloadChunk(result)
		delete(pending, index)
	}
}

func waitAndReleaseDownloadResults(done <-chan error, results <-chan downloadChunkResult) error {
	err := <-done
	for result := range results {
		releaseDownloadChunk(result)
	}
	return err
}

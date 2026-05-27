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

	"github.com/iyear/tdl/app/http/transfer"
	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/tmedia"
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

type taskStreamer func(ctx context.Context, task *downloadTask, lease *transfer.Lease, start, end int64, w io.Writer) error

type sessionManager struct {
	mu              sync.Mutex
	sessions        map[string]*downloadSession
	pools           *poolHolder
	cacheLimitBytes int64
	cacheTTL        time.Duration
	logger          *zap.Logger
}

func newSessionManager(pools *poolHolder, cacheLimitBytes int64, cacheTTL time.Duration, logger *zap.Logger) *sessionManager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &sessionManager{
		sessions:        make(map[string]*downloadSession),
		pools:           pools,
		cacheLimitBytes: cacheLimitBytes,
		cacheTTL:        cacheTTL,
		logger:          logger,
	}
}

func (m *sessionManager) Get(task *downloadTask, refresh func(ctx context.Context) (*tmedia.Media, error)) *downloadSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	session := m.sessions[task.ID]
	if session == nil {
		source := &telegramMediaSource{media: task.Media, refresh: refresh}
		session = newDownloadSession(task.ID, source, m.pools, m.cacheLimitBytes, m.cacheTTL, m, m.logger)
		m.sessions[task.ID] = session
		return session
	}
	session.source.Update(task.Media, refresh)
	return session
}

func (m *sessionManager) CleanupIdle(now time.Time, ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}

	var expired []*downloadSession
	m.mu.Lock()
	for taskID, session := range m.sessions {
		if session.IsIdle(now, ttl) {
			delete(m.sessions, taskID)
			expired = append(expired, session)
		}
	}
	m.mu.Unlock()

	for _, session := range expired {
		session.Close()
	}
	return len(expired)
}

func (m *sessionManager) prepareBufferAcquire(now time.Time) {
	if m == nil || m.cacheLimitBytes <= 0 {
		return
	}
	if m.releaseExpiredBuffers(now) > 0 {
		requestHTTPBufferMemoryReturn()
	}
	if HTTPBufferBytes() < m.cacheLimitBytes {
		return
	}
	if m.evictOldestBuffer() {
		requestHTTPBufferMemoryReturn()
	}
}

func (m *sessionManager) releaseExpiredBuffers(now time.Time) int {
	sessions := m.snapshotSessions()
	released := 0
	for _, session := range sessions {
		released += session.expireChunks(now)
	}
	return released
}

func (m *sessionManager) evictOldestBuffer() bool {
	type candidate struct {
		session *downloadSession
		key     downloadChunkKey
		usedAt  time.Time
	}

	var oldest candidate
	for _, session := range m.snapshotSessions() {
		key, usedAt, ok := session.oldestEvictableChunk()
		if !ok {
			continue
		}
		if oldest.session == nil || usedAt.Before(oldest.usedAt) {
			oldest = candidate{
				session: session,
				key:     key,
				usedAt:  usedAt,
			}
		}
	}
	if oldest.session == nil {
		return false
	}
	return oldest.session.evictChunkIfAvailable(oldest.key)
}

func (m *sessionManager) snapshotSessions() []*downloadSession {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	sessions := make([]*downloadSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	return sessions
}

type downloadChunkKey struct {
	offset int64
	limit  int
}

type downloadSessionChunk struct {
	done          chan struct{}
	data          []byte
	refs          int
	waiters       int
	lastUsed      time.Time
	accounted     bool
	releaseBuffer func()
}

type downloadSession struct {
	taskID          string
	source          *telegramMediaSource
	pools           *poolHolder
	cacheLimitBytes int64
	cacheTTL        time.Duration
	manager         *sessionManager
	logger          *zap.Logger
	reporter        TelegramFileErrorReporter

	mu          sync.Mutex
	active      int
	lastUsed    time.Time
	chunks      map[downloadChunkKey]*downloadSessionChunk
	cachedBytes int64
}

func newDownloadSession(taskID string, source *telegramMediaSource, pools *poolHolder, cacheLimitBytes int64, cacheTTL time.Duration, manager *sessionManager, logger *zap.Logger) *downloadSession {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &downloadSession{
		taskID:          taskID,
		source:          source,
		pools:           pools,
		cacheLimitBytes: cacheLimitBytes,
		cacheTTL:        cacheTTL,
		manager:         manager,
		logger:          logger,
		lastUsed:        time.Now(),
		chunks:          make(map[downloadChunkKey]*downloadSessionChunk),
	}
}

func (s *downloadSession) SetTelegramFileErrorReporter(reporter TelegramFileErrorReporter) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.reporter = reporter
}

func (s *downloadSession) telegramFileErrorReporter() TelegramFileErrorReporter {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return s.reporter
}

func streamTelegramMedia(ctx context.Context, pool dcpool.Pool, source *telegramMediaSource, lease *transfer.Lease, start, end int64, w io.Writer) error {
	holder := &poolHolder{}
	holder.Set(pool)
	session := newDownloadSession("ad-hoc", source, holder, sessionCacheLimitBytesFromLease(lease), httpBufferRetentionTTL, nil, logctx.From(ctx))
	return session.Stream(ctx, lease, start, end, w)
}

func sessionCacheLimitBytesFromLease(lease *transfer.Lease) int64 {
	if lease == nil || lease.BufferSlots() <= 0 {
		return 0
	}
	return int64(lease.BufferSlots()) * int64(downloadStreamPartSize)
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
			req: telegramChunkRequest{
				offset: reqOffset,
				limit:  int(reqEnd - reqOffset),
			},
			skip: int(needStart - reqOffset),
			take: int(needEnd - needStart + 1),
		})

		next = needEnd + 1
		index++
	}

	return jobs
}

func (s *downloadSession) beginRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.active++
	s.lastUsed = time.Now()
}

func (s *downloadSession) finishRequest() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active > 0 {
		s.active--
	}
	s.lastUsed = time.Now()
}

func (s *downloadSession) IsIdle(now time.Time, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.active == 0 && now.Sub(s.lastUsed) >= ttl
}

func (s *downloadSession) Close() {
	s.mu.Lock()
	released := 0
	for key, entry := range s.chunks {
		if s.deleteChunkLocked(key, entry) {
			released++
		}
	}
	s.mu.Unlock()
	if released > 0 {
		requestHTTPBufferMemoryReturn()
	}
}

func (s *downloadSession) Stream(ctx context.Context, lease *transfer.Lease, start, end int64, w io.Writer) error {
	s.beginRequest()
	defer s.finishRequest()

	logger := logctx.From(ctx)
	if end < start {
		return errors.New("invalid byte range")
	}

	if s.pools == nil {
		return errors.New("telegram client unavailable")
	}
	pool := s.pools.Get()
	if pool == nil {
		return errors.New("telegram client unavailable")
	}

	media := s.source.Media()
	if media == nil {
		return errors.New("telegram media is unavailable")
	}

	jobs := buildDownloadChunkJobs(start, end)
	if len(jobs) == 0 {
		return nil
	}

	flusher, _ := w.(http.Flusher)
	maxWorkers := 1
	bufferSlots := 0
	if lease != nil {
		maxWorkers = lease.MaxWorkers()
		bufferSlots = lease.BufferSlots()
	}
	if maxWorkers < 1 {
		maxWorkers = 1
	}
	workers := min(maxWorkers, len(jobs))
	if workers < 1 {
		workers = 1
	}
	resultCapacity := workers
	if bufferSlots > 0 {
		resultCapacity = min(bufferSlots, len(jobs))
	}
	if resultCapacity < 1 {
		resultCapacity = 1
	}

	results := make(chan downloadChunkResult, resultCapacity)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	reporter := s.telegramFileErrorReporter()

	logger.Info("Starting shared Telegram media stream",
		zap.Int("dc", media.DC),
		zap.Int64("media_size", media.Size),
		zap.Int64("start", start),
		zap.Int64("end", end),
		zap.Int("workers", workers),
		zap.Int("buffer_slots", bufferSlots),
		zap.Int("chunks", len(jobs)),
		zap.Int64("global_cache_limit_bytes", s.cacheLimitBytes),
		zap.Duration("cache_ttl", s.cacheTTL))

	g, gctx := errgroup.WithContext(ctx)
	jobsCh := make(chan downloadChunkJob)
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

	for i := 0; i < workers; i++ {
		g.Go(func() error {
			for {
				select {
				case <-gctx.Done():
					return gctx.Err()
				case job, ok := <-jobsCh:
					if !ok {
						return nil
					}

					key, raw, err := s.acquireChunk(gctx, pool, lease, reporter, job.req)
					if err != nil {
						return err
					}
					data, err := sliceTelegramChunk(raw, job.skip, job.take)
					if err != nil {
						s.releaseChunk(key)
						return errors.Wrap(err, "slice telegram file chunk")
					}

					result := downloadChunkResult{
						index:   job.index,
						data:    data,
						release: func() { s.releaseChunk(key) },
					}
					select {
					case <-gctx.Done():
						releaseDownloadChunk(result)
						return gctx.Err()
					case results <- result:
					}
				}
			}
		})
	}

	done := make(chan error, 1)
	go func() {
		done <- g.Wait()
		close(results)
	}()

	pending := make(map[int]downloadChunkResult, resultCapacity)
	var written int64
	next := 0
	for result := range results {
		pending[result.index] = result
		for {
			chunk, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)

			n, err := w.Write(chunk.data)
			releaseDownloadChunk(chunk)
			written += int64(n)
			if flusher != nil {
				flusher.Flush()
			}
			if err != nil {
				cancel()
				releasePendingDownloadChunks(pending)
				_ = waitAndReleaseDownloadResults(done, results)
				logger.Error("Writing HTTP response body failed",
					zap.Int("chunk_size", len(chunk.data)),
					zap.Int("written", n),
					zap.Int64("bytes_written", written),
					zap.Error(err))
				return errors.Wrap(err, "write http response")
			}
			if n != len(chunk.data) {
				cancel()
				releasePendingDownloadChunks(pending)
				_ = waitAndReleaseDownloadResults(done, results)
				logger.Error("Short write while streaming HTTP response",
					zap.Int("expected", len(chunk.data)),
					zap.Int("actual", n),
					zap.Int64("bytes_written", written))
				return io.ErrShortWrite
			}

			next++
			if next == len(jobs) {
				err := <-done
				if err != nil && !errors.Is(err, context.Canceled) {
					logger.Error("Telegram media stream failed after completion",
						zap.Int64("bytes_written", written),
						zap.Error(err))
					return errors.Wrap(err, "wait parallel workers")
				}
				logger.Info("Shared Telegram media stream completed",
					zap.Int64("bytes_written", written))
				return nil
			}
		}
	}

	err := <-done
	if err != nil {
		releasePendingDownloadChunks(pending)
		if errors.Is(err, context.Canceled) {
			logger.Warn("Telegram media stream canceled",
				zap.Int64("bytes_written", written),
				zap.Error(err))
			return err
		}
		logger.Error("Telegram media stream failed",
			zap.Int64("bytes_written", written),
			zap.Error(err))
		return errors.Wrap(err, "wait parallel workers")
	}

	if next != len(jobs) {
		releasePendingDownloadChunks(pending)
		return io.ErrUnexpectedEOF
	}

	logger.Info("Shared Telegram media stream completed",
		zap.Int64("bytes_written", written))
	return nil
}

func (s *downloadSession) acquireChunk(ctx context.Context, pool dcpool.Pool, lease *transfer.Lease, reporter TelegramFileErrorReporter, req telegramChunkRequest) (downloadChunkKey, []byte, error) {
	key := downloadChunkKey(req)
	for {
		s.mu.Lock()
		entry := s.chunks[key]
		if entry == nil {
			entry = &downloadSessionChunk{
				done:     make(chan struct{}),
				lastUsed: time.Now(),
			}
			s.chunks[key] = entry
			s.mu.Unlock()

			releaseBuffer, err := s.acquireCacheBuffer(ctx, lease)
			if err != nil {
				s.mu.Lock()
				if current := s.chunks[key]; current == entry {
					delete(s.chunks, key)
					close(entry.done)
				}
				s.mu.Unlock()
				return key, nil, err
			}

			data, err := s.source.FetchChunk(ctx, pool, lease, reporter, req)

			s.mu.Lock()
			entry.lastUsed = time.Now()
			if err != nil {
				if releaseBuffer != nil {
					releaseBuffer()
				}
				delete(s.chunks, key)
				close(entry.done)
				s.mu.Unlock()
				return key, nil, err
			}
			entry.data = data
			entry.refs = 1
			entry.releaseBuffer = releaseBuffer
			if len(data) > 0 && releaseBuffer != nil {
				entry.accounted = true
				s.cachedBytes += int64(len(data))
				recordHTTPBufferBytes(int64(len(data)))
			}
			close(entry.done)
			s.mu.Unlock()
			return key, data, nil
		}

		done := entry.done
		select {
		case <-done:
			entry.refs++
			entry.lastUsed = time.Now()
			data := entry.data
			s.mu.Unlock()
			return key, data, nil
		default:
			entry.waiters++
			s.mu.Unlock()
			select {
			case <-ctx.Done():
				s.mu.Lock()
				if current := s.chunks[key]; current == entry {
					if entry.waiters > 0 {
						entry.waiters--
					}
					if entry.refs == 0 && entry.waiters == 0 && s.cacheLimitBytes <= 0 && entry.accounted {
						s.deleteChunkLocked(key, entry)
					}
				}
				s.mu.Unlock()
				return key, nil, ctx.Err()
			case <-done:
				s.mu.Lock()
				if current := s.chunks[key]; current == entry {
					if entry.waiters > 0 {
						entry.waiters--
					}
					entry.refs++
					entry.lastUsed = time.Now()
					data := entry.data
					s.mu.Unlock()
					return key, data, nil
				}
				s.mu.Unlock()
			}
		}
	}
}

func (s *downloadSession) acquireCacheBuffer(ctx context.Context, lease *transfer.Lease) (func(), error) {
	if s.cacheLimitBytes <= 0 || lease == nil || lease.BufferSlots() <= 0 {
		return nil, nil
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()

	for {
		if release, ok := lease.TryAcquireBuffer(); ok {
			return release, nil
		}
		s.releaseBufferPressure(time.Now())
		if release, ok := lease.TryAcquireBuffer(); ok {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *downloadSession) releaseBufferPressure(now time.Time) {
	if s.manager != nil {
		s.manager.prepareBufferAcquire(now)
		return
	}
	if s.expireChunks(now) > 0 {
		requestHTTPBufferMemoryReturn()
		return
	}
	if s.evictOldestChunk() {
		requestHTTPBufferMemoryReturn()
	}
}

func (s *downloadSession) releaseChunk(key downloadChunkKey) {
	s.mu.Lock()

	entry := s.chunks[key]
	if entry == nil {
		s.mu.Unlock()
		return
	}
	if entry.refs > 0 {
		entry.refs--
	}
	entry.lastUsed = time.Now()
	if entry.refs > 0 {
		s.mu.Unlock()
		return
	}
	if entry.waiters > 0 {
		s.mu.Unlock()
		return
	}
	if s.cacheLimitBytes <= 0 || !entry.accounted || s.cacheTTL <= 0 {
		deleted := s.deleteChunkLocked(key, entry)
		s.mu.Unlock()
		if deleted {
			requestHTTPBufferMemoryReturn()
		}
		return
	}
	usedAt := entry.lastUsed
	s.mu.Unlock()
	s.scheduleChunkExpiry(key, usedAt)
}

func (s *downloadSession) scheduleChunkExpiry(key downloadChunkKey, usedAt time.Time) {
	if s.cacheTTL <= 0 {
		return
	}
	time.AfterFunc(s.cacheTTL, func() {
		if s.expireChunkIfUnused(key, usedAt, time.Now()) {
			requestHTTPBufferMemoryReturn()
		}
	})
}

func (s *downloadSession) expireChunkIfUnused(key downloadChunkKey, usedAt, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.chunks[key]
	if entry == nil || !s.chunkExpired(entry, usedAt, now) {
		return false
	}
	return s.deleteChunkLocked(key, entry)
}

func (s *downloadSession) expireChunks(now time.Time) int {
	if s.cacheTTL <= 0 {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	released := 0
	for key, entry := range s.chunks {
		if !s.chunkExpired(entry, entry.lastUsed, now) {
			continue
		}
		if s.deleteChunkLocked(key, entry) {
			released++
		}
	}
	return released
}

func (s *downloadSession) chunkExpired(entry *downloadSessionChunk, usedAt, now time.Time) bool {
	if entry.refs > 0 || entry.waiters > 0 || !entry.accounted {
		return false
	}
	if entry.lastUsed.After(usedAt) {
		return false
	}
	return !entry.lastUsed.Add(s.cacheTTL).After(now)
}

func (s *downloadSession) oldestEvictableChunk() (downloadChunkKey, time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var oldestKey downloadChunkKey
	var oldest *downloadSessionChunk
	for key, entry := range s.chunks {
		if entry.refs > 0 || entry.waiters > 0 || !entry.accounted {
			continue
		}
		if oldest == nil || entry.lastUsed.Before(oldest.lastUsed) {
			oldestKey = key
			oldest = entry
		}
	}
	if oldest == nil {
		return downloadChunkKey{}, time.Time{}, false
	}
	return oldestKey, oldest.lastUsed, true
}

func (s *downloadSession) evictChunkIfAvailable(key downloadChunkKey) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.chunks[key]
	if entry == nil || entry.refs > 0 || entry.waiters > 0 || !entry.accounted {
		return false
	}
	return s.deleteChunkLocked(key, entry)
}

func (s *downloadSession) evictOldestChunk() bool {
	key, _, ok := s.oldestEvictableChunk()
	if !ok {
		return false
	}
	return s.evictChunkIfAvailable(key)
}

func (s *downloadSession) deleteChunkLocked(key downloadChunkKey, entry *downloadSessionChunk) bool {
	delete(s.chunks, key)
	released := false
	if entry.accounted {
		n := int64(len(entry.data))
		s.cachedBytes -= n
		if s.cachedBytes < 0 {
			s.cachedBytes = 0
		}
		recordHTTPBufferBytes(-n)
		released = true
	}
	if entry.releaseBuffer != nil {
		entry.releaseBuffer()
		entry.releaseBuffer = nil
	}
	entry.data = nil
	return released
}

package httpdl

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/http/transfer"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/core/util/tutil"
	"github.com/iyear/tdl/pkg/config"
)

const (
	downloadStreamPartSize            = 1024 * 1024
	telegramGetFilePreciseAlignment   = 1024
	telegramGetFileFragmentWindowSize = 1024 * 1024
)

const (
	// telegramChunkMaxRetries bounds the last-resort, in-place retry of a single
	// upload.getFile chunk for transient conditions the lower MTProto layers do
	// not already recover (an empty body, a hung request, a connection reset that
	// survived the retry middleware). Recovering the chunk here keeps the HTTP
	// response alive instead of tearing down the whole stream and forcing the
	// client (aria2) to re-download from the start.
	telegramChunkMaxRetries     = 4
	telegramChunkRetryBaseDelay = 250 * time.Millisecond
	telegramChunkRetryMaxDelay  = 2 * time.Second
	// telegramChunkAttemptTimeout is a dead-connection backstop, NOT a throughput
	// throttle: a single ≤1 MiB getFile slower than this means < ~3.5 KiB/s, which
	// is below any usable link, so the connection is effectively dead. It is
	// deliberately far above any real per-chunk transfer time so it can never cut
	// off a slow-but-progressing download and shorten the resulting file.
	telegramChunkAttemptTimeout = 5 * time.Minute
)

const (
	httpBufferModeOff    = "off"
	httpBufferModeMemory = "memory"
)

const (
	mediaKindDocument = "document"
	mediaKindPhoto    = "photo"
)

const (
	downloadTaskKeyPrefix  = "watch.download."
	downloadTaskIndexKey   = "watch.download.index"
	defaultDownloadTaskTTL = 24 * time.Hour
	downloadSessionIdleTTL = 2 * time.Minute
	httpBufferRetentionTTL = 5 * time.Second
	telegramFileErrorTTL   = time.Minute
)

const (
	DownloadTaskKeyPrefix = downloadTaskKeyPrefix
	DownloadTaskIndexKey  = downloadTaskIndexKey
	BufferModeOff         = httpBufferModeOff
	BufferModeMemory      = httpBufferModeMemory
)

type Task = downloadTask

type TaskStore = taskStore

type PoolHolder = poolHolder

type Proxy = downloadProxy

type TaskStreamer = taskStreamer

type downloadProxy struct {
	cfg      config.HTTPConfig
	tasks    *taskStore
	pools    *poolHolder
	sessions *sessionManager
	server   *http.Server
	stream   taskStreamer
	limiter  *transfer.Limiter
	logger   *zap.Logger

	reporterMu sync.RWMutex
	reporter   TelegramFileErrorReporter
}

func newDownloadProxy(cfg config.HTTPConfig, maxFiles, maxPerFile int, pools *poolHolder, kv storage.Storage, logger *zap.Logger) *downloadProxy {
	if logger == nil {
		logger = zap.NewNop()
	}

	bufferSlots := httpMemoryBufferSlots(cfg.Buffer)
	requestSlots := config.HTTPRangeConnectionsFor(cfg, maxPerFile)
	p := &downloadProxy{
		cfg:      cfg,
		tasks:    newTaskStore(kv, downloadLinkTTL(cfg)),
		pools:    pools,
		sessions: newSessionManager(pools, int64(bufferSlots)*int64(downloadStreamPartSize), httpBufferRetentionTTL, logger.Named("watch-http-session")),
		limiter:  transfer.NewLimiter(maxFiles, maxPerFile, bufferSlots, requestSlots),
		logger:   logger.Named("watch-http"),
	}

	p.stream = p.streamTask
	p.server = &http.Server{
		Addr:    config.HTTPConfigListenAddr(cfg),
		Handler: p.routes(),
	}

	return p
}

func NewProxy(cfg config.HTTPConfig, maxFiles, maxPerFile int, pools *PoolHolder, kv storage.Storage, logger *zap.Logger) *Proxy {
	return newDownloadProxy(cfg, maxFiles, maxPerFile, pools, kv, logger)
}

func (p *downloadProxy) Tasks() *TaskStore {
	if p == nil {
		return nil
	}
	return p.tasks
}

func (p *downloadProxy) Limiter() *transfer.Limiter {
	if p == nil {
		return nil
	}
	return p.limiter
}

func (p *downloadProxy) SetTaskTTL(ttl time.Duration) {
	if p == nil || p.tasks == nil {
		return
	}
	p.tasks.SetTTL(ttl)
}

func (p *downloadProxy) SetStream(stream TaskStreamer) {
	if p == nil {
		return
	}
	p.stream = stream
}

func (p *downloadProxy) SetTelegramFileErrorReporter(reporter TelegramFileErrorReporter) {
	if p == nil {
		return
	}

	p.reporterMu.Lock()
	defer p.reporterMu.Unlock()

	p.reporter = reporter
}

func (p *downloadProxy) telegramFileErrorReporter() TelegramFileErrorReporter {
	if p == nil {
		return nil
	}

	p.reporterMu.RLock()
	defer p.reporterMu.RUnlock()

	return p.reporter
}

func (p *downloadProxy) Stream(ctx context.Context, task *Task, lease *transfer.Lease, start, end int64, w io.Writer) error {
	if p == nil {
		return errors.New("download proxy is not initialized")
	}
	if p.stream != nil {
		return p.stream(ctx, task, lease, start, end, w)
	}
	return p.streamTask(ctx, task, lease, start, end, w)
}

func (p *downloadProxy) Start(ctx context.Context) error {
	p.logger.Info("Starting HTTP download proxy",
		zap.String("listen", config.HTTPConfigListenAddr(p.cfg)),
		zap.String("public_base_url", p.cfg.PublicBaseURL),
		zap.Duration("download_link_ttl", p.tasks.ttl),
		zap.String("buffer_mode", normalizeHTTPBufferMode(p.cfg.Buffer.Mode)),
		zap.Int("buffer_size_mb", normalizedHTTPBufferSizeMB(p.cfg.Buffer)),
		zap.String("transfer_mode", p.cfg.TransferMode),
		zap.Int("range_connections", p.cfg.RangeConnections))

	p.startCleanupLoop(ctx)
	p.startSessionCleanupLoop(ctx)

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.server.Shutdown(shutdownCtx)
	}()

	return p.server.ListenAndServe()
}

func (p *downloadProxy) CleanupExpiredTasks(ctx context.Context) error {
	return p.tasks.CleanupExpired(ctx, time.Now())
}

func (p *downloadProxy) startCleanupLoop(ctx context.Context) {
	if p.tasks.kv == nil || p.tasks.ttl == 0 {
		return
	}

	cleanup := func() {
		if err := p.CleanupExpiredTasks(ctx); err != nil && !errors.Is(err, context.Canceled) {
			p.logger.Warn("Failed to clean expired download tasks", zap.Error(err))
		}
	}

	cleanup()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

func (p *downloadProxy) startSessionCleanupLoop(ctx context.Context) {
	cleanup := func() {
		if n := p.sessions.CleanupIdle(time.Now(), downloadSessionIdleTTL); n > 0 {
			p.logger.Debug("Cleaned idle HTTP download sessions", zap.Int("count", n))
			requestHTTPBufferMemoryReturn()
		}
	}

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				cleanup()
				return
			case <-ticker.C:
				cleanup()
			}
		}
	}()
}

func (p *downloadProxy) NewTask(ctx context.Context, peerID int64, msgID int, peer tg.InputPeerClass, fileName string, fileSize int64, media *tmedia.Media) (*downloadTask, error) {
	id, err := downloadTaskID(media)
	if err != nil {
		return nil, errors.Wrap(err, "build persistent download task id")
	}

	task := &downloadTask{
		ID:        id,
		PeerID:    peerID,
		MessageID: msgID,
		Peer:      peer,
		FileName:  fileName,
		FileSize:  fileSize,
		Media:     media,
		CreatedAt: time.Now(),
	}
	if err := p.tasks.Add(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (p *downloadProxy) BuildURL(taskID string) (string, error) {
	return buildDownloadURL(p.cfg.PublicBaseURL, taskID)
}

func (p *downloadProxy) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/download/", p.handleDownload)
	return mux
}

func (p *downloadProxy) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := strings.TrimPrefix(r.URL.Path, "/download/")
	if taskID == "" || strings.Contains(taskID, "/") {
		p.logger.Warn("Rejecting invalid download path",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("user_agent", r.UserAgent()))
		http.NotFound(w, r)
		return
	}

	p.logger.Info("Download request received",
		zap.String("method", r.Method),
		zap.String("task_id", taskID),
		zap.String("range", r.Header.Get("Range")),
		zap.String("remote_addr", r.RemoteAddr),
		zap.String("user_agent", r.UserAgent()))

	task, ok, err := p.tasks.Get(r.Context(), taskID)
	if err != nil {
		p.logger.Error("Failed to load download task",
			zap.String("task_id", taskID),
			zap.Error(err))
		http.Error(w, "failed to load download task", http.StatusInternalServerError)
		return
	}
	if !ok {
		p.logger.Warn("Download task not found",
			zap.String("task_id", taskID))
		http.NotFound(w, r)
		return
	}

	start, end, partial, err := parseDownloadRange(r.Header.Get("Range"), task.FileSize)
	if err != nil {
		p.logger.Warn("Invalid download range",
			zap.String("task_id", taskID),
			zap.String("range", r.Header.Get("Range")),
			zap.Error(err))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", task.FileSize))
		http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
		return
	}

	var lease *transfer.Lease
	if r.Method != http.MethodHead {
		waitStart := time.Now()
		acquired, err := p.limiter.Acquire(r.Context(), task.ID)
		if err != nil {
			fields := []zap.Field{
				zap.String("task_id", task.ID),
				zap.String("file_name", task.FileName),
				zap.Duration("waited", time.Since(waitStart)),
				zap.Error(err),
			}
			if errors.Is(err, context.Canceled) {
				p.logger.Warn("Download request canceled while waiting for slot", fields...)
				return
			}

			p.logger.Error("Failed to acquire download slot", fields...)
			http.Error(w, "failed to acquire download slot", http.StatusInternalServerError)
			return
		}
		lease = acquired
		defer lease.Release()

		if waited := time.Since(waitStart); waited >= 100*time.Millisecond {
			p.logger.Info("Download request waited for slot",
				zap.String("task_id", task.ID),
				zap.String("file_name", task.FileName),
				zap.Duration("waited", waited))
		}
	}

	contentType := mime.TypeByExtension(filepath.Ext(task.FileName))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": task.FileName})

	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))

	if partial {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, task.FileSize))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	p.logger.Info("Serving download task",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("file_size", task.FileSize),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end),
		zap.Bool("partial", partial))

	if r.Method == http.MethodHead {
		p.logger.Info("HEAD request served without body",
			zap.String("task_id", task.ID))
		return
	}

	if err := p.stream(r.Context(), task, lease, start, end, w); err != nil {
		fields := []zap.Field{
			zap.String("task_id", task.ID),
			zap.String("file_name", task.FileName),
			zap.Int64("range_start", start),
			zap.Int64("range_end", end),
			zap.Error(err),
		}
		if errors.Is(err, context.Canceled) {
			p.logger.Warn("Download client disconnected", fields...)
			return
		}

		p.logger.Error("Download stream failed", fields...)
		return
	}

	p.logger.Info("Download stream finished",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end))
}

func (p *downloadProxy) streamTask(ctx context.Context, task *downloadTask, lease *transfer.Lease, start, end int64, w io.Writer) error {
	pool := p.pools.Get()
	if pool == nil {
		err := errors.New("telegram client unavailable")
		p.logger.Error("Cannot stream download task",
			zap.String("task_id", task.ID),
			zap.Error(err))
		return err
	}

	streamCtx := logctx.With(ctx, p.logger.With(
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("file_size", task.FileSize),
		zap.Int64("range_start", start),
		zap.Int64("range_end", end),
		zap.Int("max_workers", lease.MaxWorkers()),
	))

	refresh := func(ctx context.Context) (*tmedia.Media, error) {
		p.logger.Warn("Refreshing expired Telegram file reference",
			zap.String("task_id", task.ID),
			zap.Int64("peer_id", task.PeerID),
			zap.Int("msg_id", task.MessageID))
		if err := p.refreshTaskMedia(ctx, task); err != nil {
			return nil, errors.Wrap(err, "refresh expired file reference")
		}
		refreshed, ok, err := p.tasks.Get(ctx, task.ID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errors.New("download task disappeared after media refresh")
		}
		return refreshed.Media, nil
	}

	session := p.sessions.Get(task, refresh)
	session.SetTelegramFileErrorReporter(p.telegramFileErrorReporter())
	return session.Stream(streamCtx, lease, start, end, w)
}

func (p *downloadProxy) refreshTaskMedia(ctx context.Context, task *downloadTask) error {
	if task.Peer == nil {
		return errors.New("download task peer is empty")
	}

	pool := p.pools.Get()
	if pool == nil {
		return errors.New("telegram client unavailable")
	}

	msg, err := tutil.GetSingleMessage(ctx, pool.Default(ctx), task.Peer, task.MessageID)
	if err != nil {
		return errors.Wrap(err, "get message for media refresh")
	}
	media, ok := tmedia.GetMedia(msg)
	if !ok {
		return errors.New("message no longer has media")
	}
	id, err := downloadTaskID(media)
	if err != nil {
		return err
	}
	if id != task.ID {
		return fmt.Errorf("refreshed media id changed from %q to %q", task.ID, id)
	}

	refreshed := *task
	refreshed.Media = media
	refreshed.FileSize = media.Size
	if err := p.tasks.Add(ctx, &refreshed); err != nil {
		return err
	}
	return nil
}

func isRefreshableFileReferenceError(err error) bool {
	if tgerr.Is(err, "FILE_REFERENCE_EXPIRED", "FILE_REFERENCE_INVALID", "FILE_REFERENCE_EMPTY", "FILEREF_UPGRADE_NEEDED") {
		return true
	}

	rpcErr, ok := tgerr.As(err)
	return ok && strings.HasPrefix(rpcErr.Type, "FILE_REFERENCE_")
}

func downloadTaskID(media *tmedia.Media) (string, error) {
	location, err := persistentMediaLocationFromMedia(media)
	if err != nil {
		return "", err
	}

	switch location.Kind {
	case mediaKindDocument:
		if location.ThumbSize != "" {
			return fmt.Sprintf("document_%d_%s", location.ID, safeTaskIDPart(location.ThumbSize)), nil
		}
		return fmt.Sprintf("document_%d", location.ID), nil
	case mediaKindPhoto:
		if location.ThumbSize != "" {
			return fmt.Sprintf("photo_%d_%s", location.ID, safeTaskIDPart(location.ThumbSize)), nil
		}
		return fmt.Sprintf("photo_%d", location.ID), nil
	default:
		return "", fmt.Errorf("unsupported media location kind %q", location.Kind)
	}
}

func safeTaskIDPart(v string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_")
	return replacer.Replace(v)
}

func downloadTaskStorageKey(id string) string {
	return downloadTaskKeyPrefix + id
}

func TaskStorageKey(id string) string {
	return downloadTaskStorageKey(id)
}

func buildDownloadURL(baseURL, taskID string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "parse public_base_url")
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("public_base_url must include scheme and host")
	}

	u.Path = path.Join(strings.TrimSuffix(u.Path, "/"), "download", taskID)

	return u.String(), nil
}

func downloadLinkTTL(cfg config.HTTPConfig) time.Duration {
	if cfg.DownloadLinkTTLHours <= 0 {
		return 0
	}
	return time.Duration(cfg.DownloadLinkTTLHours) * time.Hour
}

func LinkTTL(cfg config.HTTPConfig) time.Duration {
	return downloadLinkTTL(cfg)
}

func parseDownloadRange(header string, size int64) (start, end int64, partial bool, err error) {
	if size <= 0 {
		return 0, 0, false, errors.New("invalid content length")
	}
	if header == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, errors.New("invalid range unit")
	}

	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false, errors.New("multiple ranges are not supported")
	}

	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range format")
	}

	switch {
	case parts[0] == "":
		suffix, convErr := strconv.ParseInt(parts[1], 10, 64)
		if convErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid suffix range")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	case parts[1] == "":
		rangeStart, convErr := strconv.ParseInt(parts[0], 10, 64)
		if convErr != nil || rangeStart < 0 || rangeStart >= size {
			return 0, 0, false, errors.New("invalid range start")
		}
		return rangeStart, size - 1, true, nil
	default:
		rangeStart, startErr := strconv.ParseInt(parts[0], 10, 64)
		rangeEnd, endErr := strconv.ParseInt(parts[1], 10, 64)
		if startErr != nil || endErr != nil || rangeStart < 0 || rangeEnd < rangeStart || rangeStart >= size {
			return 0, 0, false, errors.New("invalid range bounds")
		}
		if rangeEnd >= size {
			rangeEnd = size - 1
		}
		return rangeStart, rangeEnd, true, nil
	}
}

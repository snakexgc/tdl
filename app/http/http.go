package httpdl

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
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

	"github.com/snakexgc/tdl/app/http/transfer"
	"github.com/snakexgc/tdl/core/logctx"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/core/tmedia"
	"github.com/snakexgc/tdl/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	downloadStreamPartSize            = 1024 * 1024
	telegramGetFilePreciseAlignment   = 1024
	telegramGetFileFragmentWindowSize = 1024 * 1024
	httpReadHeaderTimeout             = 10 * time.Second
	httpIdleTimeout                   = 2 * time.Minute
	httpMaxHeaderBytes                = 1 << 20
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
	mediaKindDocument = "document"
	mediaKindPhoto    = "photo"
)

const (
	downloadTaskKeyPrefix  = "watch.download."
	downloadTaskIndexKey   = "watch.download.index"
	defaultDownloadTaskTTL = 24 * time.Hour
	sourceRegistryIdleTTL  = 2 * time.Minute
	telegramFileErrorTTL   = time.Minute
)

const (
	DownloadTaskKeyPrefix = downloadTaskKeyPrefix
	DownloadTaskIndexKey  = downloadTaskIndexKey
)

var errRangeNoOverlap = errors.New("requested range does not overlap content")

type Task = downloadTask

type TaskStore = taskStore

type PoolHolder = poolHolder

type Proxy = downloadProxy

type TaskStreamer = taskStreamer

type downloadRange struct {
	start int64
	end   int64
}

func (r downloadRange) length() int64 {
	if r.end < r.start {
		return 0
	}
	return r.end - r.start + 1
}

type downloadProxy struct {
	cfgMu     sync.RWMutex
	cfg       config.HTTPConfig
	tasks     *taskStore
	pools     *poolHolder
	sources   *sourceRegistry
	server    *http.Server
	stream    taskStreamer
	parallel  taskStreamer
	scheduler *transfer.Scheduler
	logger    *zap.Logger

	reporterMu sync.RWMutex
	reporter   TelegramFileErrorReporter
}

func newDownloadProxy(cfg config.HTTPConfig, maxFiles, poolSize int, pools *poolHolder, kv storage.Storage, logger *zap.Logger) *downloadProxy {
	if logger == nil {
		logger = zap.NewNop()
	}
	if maxFiles < 1 {
		maxFiles = config.DefaultLimit
	}
	if poolSize < 1 {
		poolSize = config.DefaultPoolSize
	}

	p := &downloadProxy{
		cfg:       cfg,
		tasks:     newTaskStore(kv, downloadLinkTTL(cfg)),
		pools:     pools,
		sources:   newSourceRegistry(),
		scheduler: transfer.NewScheduler(maxFiles, poolSize),
		logger:    logger.Named("http-download"),
	}

	p.stream = p.streamTask
	p.parallel = p.streamTaskParallel
	setActiveScheduler(p.scheduler)
	p.server = p.newServer()

	return p
}

func (p *downloadProxy) newServer() *http.Server {
	cfg := p.config()
	return &http.Server{
		Addr:              config.HTTPConfigListenAddr(cfg),
		Handler:           p.routes(),
		ReadHeaderTimeout: httpReadHeaderTimeout,
		IdleTimeout:       httpIdleTimeout,
		MaxHeaderBytes:    httpMaxHeaderBytes,
	}
}

func (p *downloadProxy) config() config.HTTPConfig {
	p.cfgMu.RLock()
	defer p.cfgMu.RUnlock()
	return p.cfg
}

func (p *downloadProxy) updateConfig(cfg config.HTTPConfig) bool {
	p.cfgMu.Lock()
	previousListen := config.HTTPConfigListenAddr(p.cfg)
	p.cfg = cfg
	nextListen := config.HTTPConfigListenAddr(p.cfg)
	p.cfgMu.Unlock()
	return previousListen != nextListen
}

func NewProxy(cfg config.HTTPConfig, maxFiles, poolSize int, pools *PoolHolder, kv storage.Storage, logger *zap.Logger) *Proxy {
	return newDownloadProxy(cfg, maxFiles, poolSize, pools, kv, logger)
}

func (p *downloadProxy) Tasks() *TaskStore {
	if p == nil {
		return nil
	}
	return p.tasks
}

func (p *downloadProxy) Scheduler() *transfer.Scheduler {
	if p == nil {
		return nil
	}
	return p.scheduler
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
	p.parallel = stream
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

func (p *downloadProxy) Stream(ctx context.Context, task *Task, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	if p == nil {
		return errors.New("download proxy is not initialized")
	}
	if p.stream != nil {
		return p.stream(ctx, task, lease, start, end, w)
	}
	return p.streamTask(ctx, task, lease, start, end, w)
}

func (p *downloadProxy) StreamParallel(ctx context.Context, task *Task, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	if p == nil {
		return errors.New("download proxy is not initialized")
	}
	if p.parallel != nil {
		return p.parallel(ctx, task, lease, start, end, w)
	}
	return p.streamTaskParallel(ctx, task, lease, start, end, w)
}

func (p *downloadProxy) Start(ctx context.Context) error {
	cfg := p.config()
	p.logger.Info("Starting HTTP download proxy",
		zap.String("listen", config.HTTPConfigListenAddr(cfg)),
		zap.String("public_base_url", cfg.PublicBaseURL),
		zap.Duration("download_link_ttl", p.tasks.TTL()),
		zap.Int("per_dc_capacity", p.scheduler.Capacity()))

	p.startCleanupLoop(ctx)
	p.startSourceCleanupLoop(ctx)
	server := p.newServer()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	return server.ListenAndServe()
}

func (p *downloadProxy) CleanupExpiredTasks(ctx context.Context) error {
	return p.tasks.CleanupExpired(ctx, time.Now())
}

func (p *downloadProxy) startCleanupLoop(ctx context.Context) {
	if p.tasks.kv == nil || p.tasks.TTL() == 0 {
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

func (p *downloadProxy) startSourceCleanupLoop(ctx context.Context) {
	cleanup := func() {
		if n := p.sources.CleanupIdle(time.Now(), sourceRegistryIdleTTL); n > 0 {
			p.logger.Debug("Cleaned idle HTTP media sources", zap.Int("count", n))
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

	now := time.Now()
	task := &downloadTask{
		ID:           id,
		PeerID:       peerID,
		MessageID:    msgID,
		Peer:         peer,
		FileName:     fileName,
		FileSize:     fileSize,
		Media:        media,
		CreatedAt:    now,
		LastActiveAt: now,
	}
	if err := p.tasks.Add(ctx, task); err != nil {
		return nil, err
	}

	return task, nil
}

func (p *downloadProxy) BuildURL(taskID string) (string, error) {
	return buildDownloadURL(p.config().PublicBaseURL, taskID)
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

	if task.FileSize < 0 {
		p.logger.Error("Download task has invalid file size",
			zap.String("task_id", taskID),
			zap.Int64("file_size", task.FileSize))
		http.Error(w, "invalid download size", http.StatusInternalServerError)
		return
	}

	etag := downloadETag(task)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("ETag", etag)
	rangeHeader := r.Header.Get("Range")
	if ifRange := strings.TrimSpace(r.Header.Get("If-Range")); ifRange != "" && ifRange != etag {
		// The client can safely resume only the representation identified by our
		// strong ETag. A stale or date-based If-Range therefore receives the full
		// current representation, as required by HTTP range semantics.
		rangeHeader = ""
	}
	ranges, err := parseDownloadRanges(rangeHeader, task.FileSize)
	if err != nil {
		if errors.Is(err, errRangeNoOverlap) && task.FileSize == 0 {
			ranges = nil
		} else {
			p.logger.Warn("Invalid download range",
				zap.String("task_id", taskID),
				zap.String("range", r.Header.Get("Range")),
				zap.Error(err))
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", task.FileSize))
			http.Error(w, err.Error(), http.StatusRequestedRangeNotSatisfiable)
			return
		}
	}
	if downloadRangesSizeExceeds(ranges, task.FileSize) {
		// Mirroring net/http ServeContent, ignore obviously abusive or redundant
		// multi-range sets whose combined payload exceeds the representation.
		ranges = nil
	}
	partial := len(ranges) > 0
	responseRanges := ranges
	if !partial {
		responseRanges = []downloadRange{{start: 0, end: task.FileSize - 1}}
	}

	var lease *transfer.TaskLease
	if r.Method != http.MethodHead && task.FileSize > 0 {
		if task.Media == nil {
			http.Error(w, "download media is unavailable", http.StatusInternalServerError)
			return
		}
		waitStart := time.Now()
		acquired, err := p.scheduler.Acquire(r.Context(), task.ID, task.Media.DC)
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

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", disposition)
	status := http.StatusOK
	var multipartWriter *multipart.Writer
	switch len(ranges) {
	case 0:
		w.Header().Set("Content-Length", strconv.FormatInt(task.FileSize, 10))
	case 1:
		status = http.StatusPartialContent
		selected := ranges[0]
		w.Header().Set("Content-Length", strconv.FormatInt(selected.length(), 10))
		w.Header().Set("Content-Range", selected.contentRange(task.FileSize))
	default:
		status = http.StatusPartialContent
		multipartWriter = multipart.NewWriter(w)
		w.Header().Set("Content-Type", "multipart/byteranges; boundary="+multipartWriter.Boundary())
		w.Header().Set("Content-Length", strconv.FormatInt(multipartDownloadRangesSize(ranges, contentType, task.FileSize, multipartWriter.Boundary()), 10))
	}
	w.WriteHeader(status)

	p.logger.Info("Serving download task",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int64("file_size", task.FileSize),
		zap.Int("range_count", len(responseRanges)),
		zap.Bool("partial", partial))

	if r.Method == http.MethodHead {
		p.logger.Info("HEAD request served without body",
			zap.String("task_id", task.ID))
		return
	}
	if task.FileSize == 0 {
		p.logger.Info("Empty download stream finished", zap.String("task_id", task.ID))
		return
	}

	streamErr := p.streamDownloadRanges(r.Context(), task, lease, responseRanges, contentType, multipartWriter, w)
	if streamErr != nil {
		fields := []zap.Field{
			zap.String("task_id", task.ID),
			zap.String("file_name", task.FileName),
			zap.Int("range_count", len(responseRanges)),
			zap.Error(streamErr),
		}
		if errors.Is(streamErr, context.Canceled) {
			p.logger.Warn("Download client disconnected", fields...)
			return
		}

		p.logger.Error("Download stream failed", fields...)
		return
	}

	p.logger.Info("Download stream finished",
		zap.String("task_id", task.ID),
		zap.String("file_name", task.FileName),
		zap.Int("range_count", len(responseRanges)))
}

func (p *downloadProxy) streamDownloadRanges(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, ranges []downloadRange, contentType string, mw *multipart.Writer, w io.Writer) error {
	for _, selected := range ranges {
		target := w
		if mw != nil {
			part, err := mw.CreatePart(selected.mimeHeader(contentType, task.FileSize))
			if err != nil {
				return errors.Wrap(err, "create multipart download range")
			}
			target = part
		}
		if err := p.stream(ctx, task, lease, selected.start, selected.end, target); err != nil {
			return err
		}
	}
	if mw != nil {
		return errors.Wrap(mw.Close(), "close multipart download ranges")
	}
	return nil
}

func (p *downloadProxy) streamTask(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	return p.streamTaskWithMode(ctx, task, lease, start, end, w, false)
}

func (p *downloadProxy) streamTaskParallel(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
	return p.streamTaskWithMode(ctx, task, lease, start, end, w, true)
}

func (p *downloadProxy) streamTaskWithMode(ctx context.Context, task *downloadTask, lease *transfer.TaskLease, start, end int64, w io.Writer, parallel bool) error {
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
		zap.Int("dc_capacity", lease.Capacity()),
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

	handle := p.sources.Acquire(task, refresh)
	defer handle.Release()
	if parallel {
		return streamTelegramMediaParallel(streamCtx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
	}
	return streamTelegramMedia(streamCtx, pool, handle.Source(), lease, p.telegramFileErrorReporter(), start, end, w)
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
	if task.Media != nil && media.DC != task.Media.DC {
		return fmt.Errorf("refreshed media changed dc from %d to %d", task.Media.DC, media.DC)
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

func (r downloadRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.end, size)
}

func (r downloadRange) mimeHeader(contentType string, size int64) textproto.MIMEHeader {
	return textproto.MIMEHeader{
		"Content-Range": {r.contentRange(size)},
		"Content-Type":  {contentType},
	}
}

func downloadETag(task *downloadTask) string {
	if task == nil {
		return `"0"`
	}
	sum := sha256.Sum256([]byte(task.ID + ":" + strconv.FormatInt(task.FileSize, 10)))
	return fmt.Sprintf(`"%x"`, sum[:16])
}

func parseDownloadRanges(header string, size int64) ([]downloadRange, error) {
	if size < 0 {
		return nil, errors.New("invalid content length")
	}
	if strings.TrimSpace(header) == "" {
		return nil, nil
	}
	unit, spec, ok := strings.Cut(header, "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return nil, errors.New("invalid range unit")
	}

	ranges := make([]downloadRange, 0, strings.Count(spec, ",")+1)
	noOverlap := false
	for raw := range strings.SplitSeq(spec, ",") {
		raw = textproto.TrimString(raw)
		if raw == "" {
			return nil, errors.New("invalid empty range")
		}
		first, last, ok := strings.Cut(raw, "-")
		if !ok {
			return nil, errors.New("invalid range format")
		}
		first = textproto.TrimString(first)
		last = textproto.TrimString(last)
		if first == "" {
			suffix, convErr := strconv.ParseInt(last, 10, 64)
			if convErr != nil || suffix <= 0 {
				return nil, errors.New("invalid suffix range")
			}
			if suffix > size {
				suffix = size
			}
			if suffix == 0 {
				noOverlap = true
				continue
			}
			ranges = append(ranges, downloadRange{start: size - suffix, end: size - 1})
			continue
		}

		start, convErr := strconv.ParseInt(first, 10, 64)
		if convErr != nil || start < 0 {
			return nil, errors.New("invalid range start")
		}
		if start >= size {
			noOverlap = true
			continue
		}
		end := size - 1
		if last != "" {
			end, convErr = strconv.ParseInt(last, 10, 64)
			if convErr != nil || end < start {
				return nil, errors.New("invalid range bounds")
			}
			if end >= size {
				end = size - 1
			}
		}
		ranges = append(ranges, downloadRange{start: start, end: end})
	}
	if noOverlap && len(ranges) == 0 {
		return nil, errRangeNoOverlap
	}
	return ranges, nil
}

func downloadRangesSizeExceeds(ranges []downloadRange, size int64) bool {
	var total int64
	for _, selected := range ranges {
		length := selected.length()
		if length > size-total {
			return true
		}
		total += length
	}
	return false
}

type downloadCountingWriter int64

func (w *downloadCountingWriter) Write(p []byte) (int, error) {
	*w += downloadCountingWriter(len(p))
	return len(p), nil
}

func multipartDownloadRangesSize(ranges []downloadRange, contentType string, size int64, boundary string) int64 {
	var encoded downloadCountingWriter
	mw := multipart.NewWriter(&encoded)
	_ = mw.SetBoundary(boundary)
	for _, selected := range ranges {
		_, _ = mw.CreatePart(selected.mimeHeader(contentType, size))
		encoded += downloadCountingWriter(selected.length())
	}
	_ = mw.Close()
	return int64(encoded)
}

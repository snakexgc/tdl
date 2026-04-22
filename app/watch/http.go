package watch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	gotddownloader "github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/config"
)

const downloadStreamPartSize = 256 * 1024

var errStreamRangeComplete = errors.New("stream range complete")

type downloadTask struct {
	ID        string
	PeerID    int64
	MessageID int
	FileName  string
	FileSize  int64
	Media     *tmedia.Media
	CreatedAt time.Time
}

type taskStore struct {
	mu    sync.RWMutex
	tasks map[string]*downloadTask
}

func newTaskStore() *taskStore {
	return &taskStore{
		tasks: make(map[string]*downloadTask),
	}
}

func (s *taskStore) Add(task *downloadTask) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tasks[task.ID] = task
}

func (s *taskStore) Get(id string) (*downloadTask, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	task, ok := s.tasks[id]
	return task, ok
}

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

type taskStreamer func(ctx context.Context, task *downloadTask, start, end int64, w io.Writer) error

type downloadProxy struct {
	cfg    config.HTTPConfig
	tasks  *taskStore
	pools  *poolHolder
	server *http.Server
	stream taskStreamer
	logger *zap.Logger
}

func newDownloadProxy(cfg config.HTTPConfig, pools *poolHolder, logger *zap.Logger) *downloadProxy {
	if logger == nil {
		logger = zap.NewNop()
	}

	p := &downloadProxy{
		cfg:    cfg,
		tasks:  newTaskStore(),
		pools:  pools,
		logger: logger.Named("watch-http"),
	}

	p.stream = p.streamTask
	p.server = &http.Server{
		Addr:    cfg.Listen,
		Handler: p.routes(),
	}

	return p
}

func (p *downloadProxy) Start(ctx context.Context) error {
	p.logger.Info("Starting HTTP download proxy",
		zap.String("listen", p.cfg.Listen),
		zap.String("public_base_url", p.cfg.PublicBaseURL))

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = p.server.Shutdown(shutdownCtx)
	}()

	return p.server.ListenAndServe()
}

func (p *downloadProxy) NewTask(peerID int64, msgID int, fileName string, fileSize int64, media *tmedia.Media) (*downloadTask, error) {
	id, err := newTaskID()
	if err != nil {
		return nil, err
	}

	task := &downloadTask{
		ID:        id,
		PeerID:    peerID,
		MessageID: msgID,
		FileName:  fileName,
		FileSize:  fileSize,
		Media:     media,
		CreatedAt: time.Now(),
	}
	p.tasks.Add(task)

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

	task, ok := p.tasks.Get(taskID)
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

	if err := p.stream(r.Context(), task, start, end, w); err != nil {
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

func (p *downloadProxy) streamTask(ctx context.Context, task *downloadTask, start, end int64, w io.Writer) error {
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
	))

	return streamTelegramMedia(streamCtx, pool.Client(streamCtx, task.Media.DC), task.Media, start, end, w)
}

func newTaskID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", errors.Wrap(err, "generate task id")
	}
	return hex.EncodeToString(buf), nil
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

func streamTelegramMedia(ctx context.Context, client *tg.Client, media *tmedia.Media, start, end int64, w io.Writer) error {
	logger := logctx.From(ctx)
	if end < start {
		return errors.New("invalid byte range")
	}

	logger.Info("Starting Telegram media stream",
		zap.Int("dc", media.DC),
		zap.Int64("media_size", media.Size),
		zap.Int64("start", start),
		zap.Int64("end", end))

	streamWriter := newHTTPStreamWriter(w, logger, start, end-start+1)
	_, err := gotddownloader.NewDownloader().
		WithPartSize(downloadStreamPartSize).
		Download(client, media.InputFileLoc).
		Stream(ctx, streamWriter)
	if err != nil {
		if errors.Is(err, errStreamRangeComplete) {
			logger.Info("Telegram media stream completed",
				zap.Int64("bytes_written", streamWriter.Written()))
			return nil
		}
		if errors.Is(err, context.Canceled) {
			logger.Warn("Telegram media stream canceled",
				zap.Int64("bytes_written", streamWriter.Written()),
				zap.Error(err))
			return err
		}
		logger.Error("Telegram media stream failed",
			zap.Int64("bytes_written", streamWriter.Written()),
			zap.Error(err))
		return errors.Wrap(err, "stream telegram media")
	}

	logger.Info("Telegram media stream completed",
		zap.Int64("bytes_written", streamWriter.Written()))
	return nil
}

type httpStreamWriter struct {
	dst       io.Writer
	flusher   http.Flusher
	logger    *zap.Logger
	skip      int64
	remaining int64
	written   int64
}

func newHTTPStreamWriter(dst io.Writer, logger *zap.Logger, skip, remaining int64) *httpStreamWriter {
	writer := &httpStreamWriter{
		dst:       dst,
		logger:    logger,
		skip:      skip,
		remaining: remaining,
	}
	if flusher, ok := dst.(http.Flusher); ok {
		writer.flusher = flusher
	}
	return writer
}

func (w *httpStreamWriter) Write(p []byte) (int, error) {
	originalLen := len(p)
	if originalLen == 0 {
		return 0, nil
	}

	if w.skip > 0 {
		if int64(len(p)) <= w.skip {
			w.skip -= int64(len(p))
			return originalLen, nil
		}
		p = p[w.skip:]
		w.skip = 0
	}

	if w.remaining <= 0 {
		return originalLen, errStreamRangeComplete
	}

	done := false
	if int64(len(p)) > w.remaining {
		p = p[:w.remaining]
		done = true
	}

	n, err := w.dst.Write(p)
	w.written += int64(n)
	if w.flusher != nil {
		w.flusher.Flush()
	}
	if err != nil {
		w.logger.Error("Writing HTTP response body failed",
			zap.Int("chunk_size", len(p)),
			zap.Int("written", n),
			zap.Int64("bytes_written", w.written),
			zap.Error(err))
		return originalLen, errors.Wrap(err, "write http response")
	}
	if n != len(p) {
		w.logger.Error("Short write while streaming HTTP response",
			zap.Int("expected", len(p)),
			zap.Int("actual", n),
			zap.Int64("bytes_written", w.written))
		return originalLen, io.ErrShortWrite
	}

	w.remaining -= int64(n)
	if done || w.remaining <= 0 {
		return originalLen, errStreamRangeComplete
	}

	return originalLen, nil
}

func (w *httpStreamWriter) Written() int64 {
	return w.written
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

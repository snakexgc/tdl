package proxy

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	telegramClientWaitTimeout  = 30 * time.Second
	httpDeliveryPersistTimeout = 5 * time.Second
)

var errRangeNoOverlap = errors.New("requested range does not overlap content")

type Handler struct {
	life                       lifetime
	source                     ports.RangeSource
	logger                     *zap.Logger
	clientWaitTimeout          time.Duration
	configuration              atomic.Pointer[policy]
	taskChanged, sourceChanged chan struct{}
}

func New(source ports.RangeSource, logger *zap.Logger, timeout time.Duration) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		life: lifetime{ctx: context.Background()}, source: source, logger: logger, clientWaitTimeout: timeout,
		taskChanged: make(chan struct{}, 1), sourceChanged: make(chan struct{}, 1),
	}
}

type downloadRange struct{ start, end int64 }

func (r downloadRange) length() int64 {
	if r.end < r.start {
		return 0
	}
	return r.end - r.start + 1
}

func (p *Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
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

	task, transport, ok, err := p.source.Open(r.Context(), taskID)
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

	etag := ETag(task)
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

	if r.Method != http.MethodHead && task.FileSize > 0 {
		if !task.Available {
			http.Error(w, "download media is unavailable", http.StatusInternalServerError)
			return
		}
		waitTimeout := p.policy().wait
		if waitTimeout <= 0 {
			waitTimeout = telegramClientWaitTimeout
		}
		waitStart := time.Now()
		waitCtx, cancel := context.WithTimeout(r.Context(), waitTimeout)
		waitErr := transport.Ready(waitCtx)
		cancel()
		if waitErr != nil {
			fields := []zap.Field{
				zap.String("task_id", task.ID),
				zap.String("file_name", task.FileName),
				zap.Duration("waited", time.Since(waitStart)),
				zap.Error(waitErr),
			}
			if r.Context().Err() != nil {
				p.logger.Warn("Download client disconnected while waiting for Telegram", fields...)
				return
			}
			p.logger.Warn("Telegram download backend is not ready", fields...)
			w.Header().Set("Retry-After", "3")
			http.Error(w, "telegram download backend is not ready", http.StatusServiceUnavailable)
			return
		}
	}

	var lease ports.DownloadLease
	if r.Method != http.MethodHead && task.FileSize > 0 {
		waitStart := time.Now()
		acquired, err := transport.Acquire(r.Context())
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
		p.recordHTTPDelivery(context.WithoutCancel(r.Context()), task, nil, transport)
		return
	}

	streamErr := p.streamDownloadRanges(r.Context(), task, transport, lease, responseRanges, contentType, multipartWriter, w)
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
	p.recordHTTPDelivery(context.WithoutCancel(r.Context()), task, responseRanges, transport)
}

func (p *Handler) recordHTTPDelivery(ctx context.Context, task types.RangeResource, ranges []downloadRange, transport ports.RangeTransfer) {
	persistCtx, cancel := context.WithTimeout(ctx, p.policy().persist)
	defer cancel()
	spans := make([]types.ByteRange, 0, len(ranges))
	for _, span := range ranges {
		spans = append(spans, types.ByteRange{Start: span.start, End: span.end})
	}
	if err := transport.Report(persistCtx, spans); err != nil {
		p.logger.Warn("Failed to persist HTTP delivery status", zap.String("task_id", task.ID), zap.Error(err))
	}
}

func (p *Handler) streamDownloadRanges(ctx context.Context, task types.RangeResource, transport ports.RangeTransfer, lease ports.DownloadLease, ranges []downloadRange, contentType string, mw *multipart.Writer, w io.Writer) error {
	for _, selected := range ranges {
		target := w
		if mw != nil {
			part, err := mw.CreatePart(selected.mimeHeader(contentType, task.FileSize))
			if err != nil {
				return errors.Wrap(err, "create multipart download range")
			}
			target = part
		}
		if err := transport.Stream(ctx, lease, selected.start, selected.end, target); err != nil {
			return err
		}
	}
	if mw != nil {
		return errors.Wrap(mw.Close(), "close multipart download ranges")
	}
	return nil
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

func ETag(task types.RangeResource) string {
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

package httpdl

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"go.uber.org/zap"

	"github.com/iyear/tdl/app/http/transfer"
	"github.com/iyear/tdl/core/dcpool"
	"github.com/iyear/tdl/core/logctx"
	"github.com/iyear/tdl/core/tmedia"
)

type telegramMediaSource struct {
	mu        sync.RWMutex
	media     *tmedia.Media
	refresh   func(ctx context.Context) (*tmedia.Media, error)
	refreshMu sync.Mutex
}

func (s *telegramMediaSource) Media() *tmedia.Media {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.media
}

func (s *telegramMediaSource) Update(media *tmedia.Media, refresh func(ctx context.Context) (*tmedia.Media, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if media != nil {
		s.media = media
	}
	s.refresh = refresh
}

func (s *telegramMediaSource) refreshFunc() func(ctx context.Context) (*tmedia.Media, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.refresh
}

func (s *telegramMediaSource) FetchChunk(ctx context.Context, pool dcpool.Pool, lease *transfer.Lease, reporter TelegramFileErrorReporter, req telegramChunkRequest) ([]byte, error) {
	for {
		media := s.Media()
		if media == nil {
			return nil, errors.New("telegram media is unavailable")
		}

		if lease != nil {
			if err := lease.AcquireWorker(ctx); err != nil {
				return nil, err
			}
		}
		client := pool.Client(ctx, media.DC)
		data, err := fetchTelegramMediaChunk(ctx, client, media, req)
		if lease != nil {
			lease.ReleaseWorker()
		}
		if err == nil {
			return data, nil
		}
		refresh := s.refreshFunc()
		if !isRefreshableFileReferenceError(err) || refresh == nil {
			reportTelegramFileError(ctx, reporter, err)
			return nil, err
		}
		if err := s.refreshMedia(ctx, media); err != nil {
			return nil, err
		}
	}
}

func reportTelegramFileError(ctx context.Context, reporter TelegramFileErrorReporter, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	recordTelegramFileError()
	if reporter == nil {
		return
	}
	reporter.ReportTelegramFileError(ctx, err)
}

func (s *telegramMediaSource) refreshMedia(ctx context.Context, current *tmedia.Media) error {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	if latest := s.Media(); latest != current {
		return nil
	}

	refresh := s.refreshFunc()
	if refresh == nil {
		return errors.New("telegram media refresh is unavailable")
	}
	next, err := refresh(ctx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.media = next
	s.mu.Unlock()
	return nil
}

type telegramChunkRequest struct {
	offset int64
	limit  int
}

func fetchTelegramMediaChunk(ctx context.Context, client *tg.Client, media *tmedia.Media, chunkReq telegramChunkRequest) ([]byte, error) {
	logger := logctx.From(ctx)

	transientRetries := 0
	backoff := telegramChunkRetryBaseDelay
	// retryTransient backs off and reports whether a bounded transient retry is
	// still allowed. A non-nil error means the parent context ended while waiting.
	retryTransient := func(reason string, cause error) (bool, error) {
		if transientRetries >= telegramChunkMaxRetries {
			return false, nil
		}
		transientRetries++
		logger.Debug("Retrying transient telegram file chunk",
			zap.String("reason", reason),
			zap.Int64("offset", chunkReq.offset),
			zap.Int("limit", chunkReq.limit),
			zap.Int("retry", transientRetries),
			zap.Duration("backoff", backoff),
			zap.Error(cause))
		if err := sleepWithContext(ctx, backoff); err != nil {
			return false, err
		}
		backoff = nextChunkBackoff(backoff)
		return true, nil
	}

	for attempt := 0; ; attempt++ {
		req := &tg.UploadGetFileRequest{
			Location: media.InputFileLoc,
			Offset:   chunkReq.offset,
			Limit:    chunkReq.limit,
		}
		req.SetPrecise(true)

		// A per-attempt deadline turns a hung request into a retryable timeout
		// instead of an indefinite 0 B/s stall.
		attemptCtx, cancel := context.WithTimeout(ctx, telegramChunkAttemptTimeout)
		finish := beginTelegramFileRequest()
		resp, err := client.UploadGetFile(attemptCtx, req)
		finish()
		cancel()

		// Real client cancellation/deadline on the parent context is never retried.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}

		if flood, waitErr := tgerr.FloodWait(ctx, err); waitErr != nil {
			if flood || tgerr.Is(waitErr, tg.ErrTimeout) {
				logger.Debug("Retrying telegram file chunk",
					zap.Int64("offset", chunkReq.offset),
					zap.Int("limit", chunkReq.limit),
					zap.Int("attempt", attempt+1),
					zap.Error(waitErr))
				continue
			}
			if isTransientChunkError(waitErr) {
				retried, retryErr := retryTransient("rpc", waitErr)
				if retryErr != nil {
					return nil, retryErr
				}
				if retried {
					continue
				}
			}
			return nil, errors.Wrap(waitErr, "get telegram file chunk")
		}

		file, ok := resp.(*tg.UploadFile)
		if !ok {
			return nil, fmt.Errorf("unexpected telegram file response %T", resp)
		}
		if len(file.Bytes) == 0 {
			// Jobs are clamped within [0, fileSize), so an empty body at a valid
			// offset is a transient anomaly, not true EOF — retry before failing.
			retried, retryErr := retryTransient("empty", io.ErrUnexpectedEOF)
			if retryErr != nil {
				return nil, retryErr
			}
			if retried {
				continue
			}
			return nil, io.ErrUnexpectedEOF
		}

		recordTelegramDownloadedBytes(len(file.Bytes))
		// gotd already decodes UploadFile.Bytes into an owned slice; avoid copying
		// every 1 MiB chunk again before it enters the session cache.
		return file.Bytes, nil
	}
}

// isTransientChunkError reports whether a chunk fetch failure is worth a bounded
// in-place retry. It is consulted only after the parent context is confirmed
// alive, so a context error here is the per-attempt timeout backstop or an
// internal MTProto engine reset surfaced as cancellation — both recoverable.
// The allowlist is intentionally narrow: anything not listed fails fast.
func isTransientChunkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return false
}

func nextChunkBackoff(current time.Duration) time.Duration {
	next := current * 2
	if next > telegramChunkRetryMaxDelay {
		return telegramChunkRetryMaxDelay
	}
	return next
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func sliceTelegramChunk(data []byte, skip, take int) ([]byte, error) {
	if skip < 0 || take < 0 {
		return nil, errors.New("invalid telegram chunk slice")
	}
	end := skip + take
	if end > len(data) {
		return nil, io.ErrUnexpectedEOF
	}
	return data[skip:end], nil
}

func alignDown(value int64, alignment int64) int64 {
	if alignment <= 0 {
		return value
	}
	return value - value%alignment
}

func alignUp(value int64, alignment int64) int64 {
	if alignment <= 0 {
		return value
	}
	if value%alignment == 0 {
		return value
	}
	return value + alignment - value%alignment
}

package httpdl

import (
	"context"
	"fmt"
	"io"
	"sync"

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

	for attempt := 0; ; attempt++ {
		req := &tg.UploadGetFileRequest{
			Location: media.InputFileLoc,
			Offset:   chunkReq.offset,
			Limit:    chunkReq.limit,
		}
		req.SetPrecise(true)

		finish := beginTelegramFileRequest()
		resp, err := client.UploadGetFile(ctx, req)
		finish()
		if flood, waitErr := tgerr.FloodWait(ctx, err); waitErr != nil {
			if flood || tgerr.Is(waitErr, tg.ErrTimeout) {
				logger.Debug("Retrying telegram file chunk",
					zap.Int64("offset", chunkReq.offset),
					zap.Int("limit", chunkReq.limit),
					zap.Int("attempt", attempt+1),
					zap.Error(waitErr))
				continue
			}
			return nil, errors.Wrap(waitErr, "get telegram file chunk")
		}

		file, ok := resp.(*tg.UploadFile)
		if !ok {
			return nil, fmt.Errorf("unexpected telegram file response %T", resp)
		}
		if len(file.Bytes) == 0 {
			return nil, io.ErrUnexpectedEOF
		}

		recordTelegramDownloadedBytes(len(file.Bytes))
		// gotd already decodes UploadFile.Bytes into an owned slice; avoid copying
		// every 1 MiB chunk again before it enters the session cache.
		return file.Bytes, nil
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

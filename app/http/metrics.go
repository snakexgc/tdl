package httpdl

import (
	"context"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type TelegramFileErrorReporter interface {
	ReportTelegramFileError(ctx context.Context, err error)
}

var (
	telegramDownloadedBytes atomic.Int64
	httpBufferBytes         atomic.Int64
	activeTelegramRequests  atomic.Int64
	telegramFileErrors      atomic.Int64
	telegramFileErrorMu     sync.Mutex
	telegramFileErrorTimes  []time.Time
	httpBufferFreeMu        sync.Mutex
	httpBufferFreeTimer     *time.Timer
)

func TelegramDownloadedBytes() int64 {
	return telegramDownloadedBytes.Load()
}

func HTTPBufferBytes() int64 {
	n := httpBufferBytes.Load()
	if n < 0 {
		return 0
	}
	return n
}

func ActiveTelegramFileRequests() int64 {
	n := activeTelegramRequests.Load()
	if n < 0 {
		return 0
	}
	return n
}

func TelegramFileErrorCount() int64 {
	n := telegramFileErrors.Load()
	if n < 0 {
		return 0
	}
	return n
}

func TelegramFileErrorCountSince(window time.Duration) int64 {
	if window <= 0 {
		return 0
	}
	now := time.Now()
	cutoff := now.Add(-window)

	telegramFileErrorMu.Lock()
	defer telegramFileErrorMu.Unlock()

	pruneTelegramFileErrorTimesLocked(now.Add(-telegramFileErrorTTL))
	var count int64
	for _, at := range telegramFileErrorTimes {
		if !at.Before(cutoff) {
			count++
		}
	}
	return count
}

func recordTelegramDownloadedBytes(n int) {
	if n <= 0 {
		return
	}
	telegramDownloadedBytes.Add(int64(n))
}

func recordHTTPBufferBytes(delta int64) {
	if delta == 0 {
		return
	}
	httpBufferBytes.Add(delta)
}

func beginTelegramFileRequest() func() {
	activeTelegramRequests.Add(1)
	return func() {
		if activeTelegramRequests.Add(-1) < 0 {
			activeTelegramRequests.Store(0)
		}
	}
}

func recordTelegramFileError() {
	telegramFileErrors.Add(1)
	now := time.Now()

	telegramFileErrorMu.Lock()
	defer telegramFileErrorMu.Unlock()

	telegramFileErrorTimes = append(telegramFileErrorTimes, now)
	pruneTelegramFileErrorTimesLocked(now.Add(-telegramFileErrorTTL))
}

func pruneTelegramFileErrorTimesLocked(cutoff time.Time) {
	idx := 0
	for idx < len(telegramFileErrorTimes) && telegramFileErrorTimes[idx].Before(cutoff) {
		idx++
	}
	if idx == 0 {
		return
	}
	copy(telegramFileErrorTimes, telegramFileErrorTimes[idx:])
	telegramFileErrorTimes = telegramFileErrorTimes[:len(telegramFileErrorTimes)-idx]
}

func requestHTTPBufferMemoryReturn() {
	httpBufferFreeMu.Lock()
	defer httpBufferFreeMu.Unlock()

	if httpBufferFreeTimer != nil {
		return
	}
	httpBufferFreeTimer = time.AfterFunc(250*time.Millisecond, func() {
		debug.FreeOSMemory()

		httpBufferFreeMu.Lock()
		httpBufferFreeTimer = nil
		httpBufferFreeMu.Unlock()
	})
}

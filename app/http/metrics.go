package httpdl

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/snakexgc/tdl/app/http/transfer"
)

type TelegramFileErrorReporter interface {
	ReportTelegramFileError(ctx context.Context, err error)
}

var (
	telegramDownloadedBytes atomic.Int64
	activeTelegramRequests  atomic.Int64
	telegramFileErrors      atomic.Int64
	telegramFileErrorMu     sync.Mutex
	telegramFileErrorTimes  []time.Time
	activeSchedulerMu       sync.RWMutex
	activeScheduler         *transfer.Scheduler
)

func TelegramDownloadedBytes() int64 {
	return telegramDownloadedBytes.Load()
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

func setActiveScheduler(s *transfer.Scheduler) {
	activeSchedulerMu.Lock()
	activeScheduler = s
	activeSchedulerMu.Unlock()
}

func DCSchedulerSnapshots() []transfer.DCSnapshot {
	activeSchedulerMu.RLock()
	s := activeScheduler
	activeSchedulerMu.RUnlock()
	if s == nil {
		return nil
	}
	return s.Snapshots()
}

package comif

import (
	"context"
	"sync"
)

// fileLimiter counts leases instead of replacing a token channel on resize.
// Old leases therefore always release into the same accounting domain.
type fileLimiter struct {
	mu          sync.Mutex
	limit, used int
	changed     chan struct{}
}

func newFileLimiter(limit int) *fileLimiter {
	return &fileLimiter{limit: limit, changed: make(chan struct{})}
}

func (l *fileLimiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if err := ctx.Err(); err != nil {
			l.mu.Unlock()
			return err
		}
		if l.used < l.limit {
			l.used++
			l.mu.Unlock()
			return nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (l *fileLimiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used == 0 {
		panic("download file token over-release")
	}
	l.used--
	l.signal()
}

func (l *fileLimiter) resize(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
	l.signal()
}

func (l *fileLimiter) signal() {
	close(l.changed)
	l.changed = make(chan struct{})
}

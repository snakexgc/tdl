package comif

import (
	"context"
	"sync"
	"sync/atomic"
)

// fileLimiter counts leases instead of replacing a token channel on resize.
// Old leases therefore always release into the same accounting domain.
type fileLimiter struct {
	mu          sync.Mutex
	limit, used int
	waiters     []*fileWaiter
}

type fileWaiter struct {
	priority *atomic.Int64
	ready    chan struct{}
	granted  bool
}

func newFileLimiter(limit int) *fileLimiter {
	return &fileLimiter{limit: limit}
}

func (l *fileLimiter) acquire(ctx context.Context, priority *atomic.Int64) error {
	l.mu.Lock()
	if err := ctx.Err(); err != nil {
		l.mu.Unlock()
		return err
	}
	waiter := &fileWaiter{priority: priority, ready: make(chan struct{})}
	l.waiters = append(l.waiters, waiter)
	l.dispatch()
	l.mu.Unlock()
	select {
	case <-waiter.ready:
		if err := ctx.Err(); err == nil {
			return nil
		}
	case <-ctx.Done():
	}
	l.mu.Lock()
	if waiter.granted {
		l.used--
	} else {
		for i, candidate := range l.waiters {
			if candidate == waiter {
				l.waiters = append(l.waiters[:i], l.waiters[i+1:]...)
				break
			}
		}
	}
	l.dispatch()
	l.mu.Unlock()
	return ctx.Err()
}

func (l *fileLimiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used == 0 {
		panic("download file token over-release")
	}
	l.used--
	l.dispatch()
}

func (l *fileLimiter) resize(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
	l.dispatch()
}

// Highest priority wins; equal-priority files retain their arrival order.
// A promotion changes the atomic priority of the already waiting task.
func (l *fileLimiter) dispatch() {
	for l.used < l.limit && len(l.waiters) > 0 {
		selected := 0
		for i, waiter := range l.waiters {
			if waiter.priority.Load() > l.waiters[selected].priority.Load() {
				selected = i
			}
		}
		waiter := l.waiters[selected]
		l.waiters = append(l.waiters[:selected], l.waiters[selected+1:]...)
		l.used++
		waiter.granted = true
		close(waiter.ready)
	}
}

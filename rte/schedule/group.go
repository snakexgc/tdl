// Package schedule owns cancellable, named component runnables.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Group struct {
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	closed bool
	names  map[string]bool
	wg     sync.WaitGroup
}

func New(ctx context.Context) *Group {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{ctx: ctx, cancel: cancel, names: make(map[string]bool)}
}

// Run schedules a one-shot (period == 0) or periodic runnable. Invocations of
// the same runnable never overlap; overruns coalesce instead of spawning work.
func (g *Group) Run(name string, delay, period time.Duration, fn func(context.Context) error, report func(error)) error {
	if name == "" || delay < 0 || period < 0 || fn == nil {
		return errors.New("invalid runnable")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed || g.ctx.Err() != nil {
		return errors.New("runnable group is stopped")
	}
	if g.names[name] {
		return fmt.Errorf("duplicate runnable %s", name)
	}
	g.names[name] = true
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-g.ctx.Done():
				return
			case <-timer.C:
			}
			if g.ctx.Err() != nil {
				return
			}
			if err := invoke(func() error { return fn(g.ctx) }); err != nil && report != nil {
				_ = invoke(func() error { report(err); return nil })
			}
			if period == 0 {
				return
			}
			timer.Reset(period)
		}
	}()
	return nil
}

func (g *Group) Stop(ctx context.Context) error {
	g.mu.Lock()
	g.closed = true
	g.cancel()
	g.mu.Unlock()
	done := make(chan struct{})
	go func() { g.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func invoke(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("runnable panic: %v", recovered)
		}
	}()
	return fn()
}

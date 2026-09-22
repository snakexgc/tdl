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
	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	closed  bool
	names   map[string]bool
	wg      sync.WaitGroup
	observe func(string, error)
	states  map[string]Status
}

type Status struct {
	Name       string        `json:"name"`
	Running    bool          `json:"running"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Runs       uint64        `json:"runs"`
	LastError  string        `json:"last_error,omitempty"`
	Period     time.Duration `json:"period_ns"`
	Overdue    bool          `json:"overdue"`
}

func New(ctx context.Context) *Group {
	return NewObserved(ctx, nil)
}

func NewObserved(ctx context.Context, observe func(string, error)) *Group {
	ctx, cancel := context.WithCancel(ctx)
	return &Group{ctx: ctx, cancel: cancel, names: make(map[string]bool), states: make(map[string]Status), observe: observe}
}

// Statuses observes periodic overruns without killing or duplicating work.
func (g *Group) Statuses() []Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	result := make([]Status, 0, len(g.states))
	for _, state := range g.states {
		state.Overdue = state.Running && state.Period > 0 && time.Since(state.StartedAt) > state.Period
		result = append(result, state)
	}
	return result
}

// Run schedules a one-shot (period == 0) or periodic runnable. Invocations of
// the same runnable never overlap; overruns coalesce instead of spawning work.
func (g *Group) Run(name string, delay, period time.Duration, fn func(context.Context) error, report func(error)) error {
	return g.run(name, delay, period, nil, nil, fn, report)
}

// RunDynamic preserves diagnostics and non-overlap while a component changes
// its period. A change resets the next deadline; it never cancels active work.
func (g *Group) RunDynamic(name string, delay time.Duration, period func() time.Duration, changed <-chan struct{}, fn func(context.Context) error, report func(error)) error {
	if period == nil || period() <= 0 {
		return errors.New("invalid dynamic runnable period")
	}
	return g.run(name, delay, period(), period, changed, fn, report)
}

func (g *Group) run(name string, delay, period time.Duration, currentPeriod func() time.Duration, changed <-chan struct{}, fn func(context.Context) error, report func(error)) error {
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
	g.states[name] = Status{Name: name, Period: period}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		timer := time.NewTimer(delay)
		defer timer.Stop()
		for {
			select {
			case <-g.ctx.Done():
				return
			case <-changed:
				if currentPeriod != nil {
					period = currentPeriod()
					timer.Reset(period)
				}
				continue
			case <-timer.C:
			}
			if g.ctx.Err() != nil {
				return
			}
			g.mu.Lock()
			state := g.states[name]
			state.Period = period
			state.Running, state.StartedAt = true, time.Now()
			g.states[name] = state
			g.mu.Unlock()
			err := invoke(func() error { return fn(g.ctx) })
			g.mu.Lock()
			state.Running, state.FinishedAt = false, time.Now()
			state.Runs++
			state.LastError = ""
			if err != nil {
				state.LastError = err.Error()
			}
			g.states[name] = state
			g.mu.Unlock()
			if err != nil {
				if g.observe != nil {
					_ = invoke(func() error { g.observe(name, err); return nil })
				}
				if report != nil {
					_ = invoke(func() error { report(err); return nil })
				}
			}
			if currentPeriod != nil {
				period = currentPeriod()
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

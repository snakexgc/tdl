package rte

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/snakexgc/tdl/bsw/services/dem"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/schedule"
)

// Recovery is opt-in. Only a completed, explicitly retryable invocation can
// restart; a hung invocation is never duplicated or forcefully replaced.
type Recovery struct {
	MaxRestarts int
	Delay       time.Duration
	Retryable   func(error) bool
}

// Process owns a dynamically enabled transport adapter. Configuration and
// account resources are captured by the caller's Run function, not globals here.
type Process struct {
	parent      context.Context
	account     types.AccountID
	id          string
	mu          sync.Mutex
	cancel      context.CancelFunc
	done        chan struct{}
	runnable    schedule.Status
	active      bool
	state       State
	lastErr     error
	diagnostics *dem.Store
}

func NewProcess(parent context.Context, account types.AccountID, id string) *Process {
	if parent == nil {
		parent = context.Background()
	}
	if account == "" {
		account = types.DefaultAccount
	}
	return &Process{parent: parent, account: account, id: id, state: Stopped, diagnostics: dem.New(account, 128)}
}

func (p *Process) Start(run func(context.Context) error, recovery Recovery) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active {
		return false, nil
	}
	if err := p.parent.Err(); err != nil {
		return false, err
	}
	if run == nil || recovery.MaxRestarts < 0 || recovery.Delay < 0 {
		return false, errors.New("invalid process configuration")
	}
	p.active, p.state, p.lastErr = true, Starting, nil
	ctx, cancel := context.WithCancel(p.parent)
	p.cancel = cancel
	p.done = make(chan struct{})
	p.runnable = schedule.Status{Name: p.id, Running: true, StartedAt: time.Now()}
	go func() {
		p.mu.Lock()
		if p.state == Starting {
			p.state = Running
		}
		p.mu.Unlock()
		var err error
		defer func() {
			if value := recover(); value != nil {
				err = fmt.Errorf("process recovery panic: %v", value)
				p.record("recovery", err)
			}
			cancel()
			p.mu.Lock()
			defer p.mu.Unlock()
			p.active = false
			p.runnable.Running = false
			p.runnable.FinishedAt = time.Now()
			if err != nil {
				p.runnable.LastError = err.Error()
			}
			close(p.done)
			p.state = Stopped
			p.lastErr = err
			if err != nil {
				p.state = Failed
			}
		}()
		for attempt := 0; ; attempt++ {
			p.mu.Lock()
			p.runnable.Runs++
			p.mu.Unlock()
			err = invokeProcess(ctx, run)
			if ctx.Err() != nil {
				err = nil
				return
			}
			if err == nil {
				return
			}
			p.record("run", err)
			if attempt >= recovery.MaxRestarts || recovery.Retryable == nil || !recovery.Retryable(err) {
				return
			}
			delay := recovery.Delay
			if delay == 0 {
				delay = time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				err = nil
				return
			case <-timer.C:
			}
		}
	}()
	return true, nil
}

func invokeProcess(ctx context.Context, run func(context.Context) error) (err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("process panic: %v", value)
		}
	}()
	return run(ctx)
}

func (p *Process) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	cancel, done := p.cancel, p.done
	if p.active {
		p.state = Stopping
	}
	p.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		p.record("stop", ctx.Err())
		return ctx.Err()
	}
}

func (p *Process) Running() bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.active
}

func (p *Process) LastError() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastErr
}

func (p *Process) Health() Health {
	p.mu.Lock()
	defer p.mu.Unlock()
	status := ComponentHealth{Status: Status{ID: p.id, State: p.state}, Runnables: []schedule.Status{}}
	if p.lastErr != nil {
		status.Detail = p.lastErr.Error()
	}
	if p.done != nil {
		status.Runnables = []schedule.Status{p.runnable}
	}
	return Health{Account: p.account, Components: []ComponentHealth{status}, Events: p.diagnostics.Events()}
}

func (p *Process) record(operation string, err error) {
	p.diagnostics.Report(p.id, operation, err)
}

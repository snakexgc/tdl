package watch

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"
)

const controllerStopTimeout = 10 * time.Second

var runControllerWatch = Run

type Controller struct {
	parent context.Context
	opts   Options
	notify NotifyFunc

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	lastErr error
}

func NewController(parent context.Context, opts Options, notify NotifyFunc) *Controller {
	if parent == nil {
		parent = context.Background()
	}
	if opts.Template == "" {
		opts = DefaultOptions(nil)
	}
	return &Controller{
		parent: parent,
		opts:   opts,
		notify: notify,
	}
}

func (c *Controller) Start() bool {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}

	ctx, cancel := context.WithCancel(c.parent)
	done := make(chan struct{})
	opts := c.opts
	opts.Notify = c.notify
	c.running = true
	c.cancel = cancel
	c.done = done
	c.lastErr = nil
	c.mu.Unlock()

	go func() {
		err := runControllerWatch(ctx, opts)
		cancel()

		if err != nil && !stderrors.Is(err, context.Canceled) && c.notify != nil {
			c.notify(context.Background(), fmt.Sprintf("监听下载已停止：%v\n请检查配置或重新登录。", err))
		}

		c.mu.Lock()
		if c.done == done {
			c.running = false
			c.cancel = nil
			c.done = nil
			if err != nil && !stderrors.Is(err, context.Canceled) {
				c.lastErr = err
			}
		}
		c.mu.Unlock()

		close(done)
	}()

	return true
}

func (c *Controller) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	done := c.done
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done == nil {
		return
	}

	timer := time.NewTimer(controllerStopTimeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
	}
}

func (c *Controller) UpdateOptions(opts Options) {
	if opts.Template == "" {
		opts = DefaultOptions(nil)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts = opts
}

func (c *Controller) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Controller) LastError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

package bot

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	"github.com/iyear/tdl/app/watch"
)

const watchControllerStopTimeout = 10 * time.Second

var runWatch = watch.Run

type watchController struct {
	parent context.Context
	opts   watch.Options
	notify watch.NotifyFunc

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
}

func newWatchController(parent context.Context, opts watch.Options, notify watch.NotifyFunc) *watchController {
	if parent == nil {
		parent = context.Background()
	}
	if opts.Template == "" {
		opts = watch.DefaultOptions(nil)
	}
	return &watchController{
		parent: parent,
		opts:   opts,
		notify: notify,
	}
}

func (c *watchController) Start() bool {
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
	c.mu.Unlock()

	go func() {
		err := runWatch(ctx, opts)
		cancel()

		if err != nil && !stderrors.Is(err, context.Canceled) && c.notify != nil {
			c.notify(context.Background(), fmt.Sprintf("watch 流程已停止：%v\n请检查配置或重新登录。", err))
		}

		c.mu.Lock()
		if c.done == done {
			c.running = false
			c.cancel = nil
			c.done = nil
		}
		c.mu.Unlock()

		close(done)
	}()

	return true
}

func (c *watchController) Stop() {
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

	timer := time.NewTimer(watchControllerStopTimeout)
	defer timer.Stop()

	select {
	case <-done:
	case <-timer.C:
	}
}

func (c *watchController) UpdateOptions(opts watch.Options) {
	if opts.Template == "" {
		opts = watch.DefaultOptions(nil)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.opts = opts
}

func (c *watchController) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

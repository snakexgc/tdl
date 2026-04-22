package bot

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"

	"github.com/iyear/tdl/app/watch"
)

type watchController struct {
	parent context.Context
	opts   watch.Options
	notify watch.NotifyFunc

	mu      sync.Mutex
	cancel  context.CancelFunc
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
	opts := c.opts
	opts.Notify = c.notify
	c.running = true
	c.cancel = cancel
	c.mu.Unlock()

	go func() {
		err := watch.Run(ctx, opts)
		cancel()

		c.mu.Lock()
		c.running = false
		c.cancel = nil
		c.mu.Unlock()

		if err != nil && !stderrors.Is(err, context.Canceled) && c.notify != nil {
			c.notify(context.Background(), fmt.Sprintf("watch 流程已停止：%v\n请检查配置或重新登录。", err))
		}
	}()

	return true
}

func (c *watchController) Stop() {
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func (c *watchController) Running() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

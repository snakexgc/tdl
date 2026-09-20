package watch

import (
	"context"
	stderrors "errors"
	"fmt"
	"sync"
	"time"

	"github.com/snakexgc/tdl/rte"
)

const controllerStopTimeout = 10 * time.Second

var runControllerWatch = Run

type Controller struct {
	process *rte.Process
	opts    Options
	notify  NotifyFunc

	mu      sync.Mutex
	done    chan struct{}
	running bool
	lastErr error
	submit  chan messageLinkSubmission
}

func NewController(parent context.Context, opts Options, notify NotifyFunc) *Controller {
	if parent == nil {
		parent = context.Background()
	}
	return &Controller{
		process: rte.NewProcess(parent, opts.Account, "host.watch"),
		opts:    opts,
		notify:  notify,
	}
}

func (c *Controller) Start() bool {
	c.mu.Lock()
	if c.running || c.process.Running() {
		c.mu.Unlock()
		return false
	}

	done := make(chan struct{})
	submit := make(chan messageLinkSubmission, 100)
	opts := c.opts
	opts.Notify = c.notify
	opts.messageLinks = submit
	if opts.Filter == nil || opts.Naming == nil {
		c.lastErr = fmt.Errorf("filter and naming ports are required")
		c.mu.Unlock()
		return false
	}
	c.running = true
	c.done = done
	c.lastErr = nil
	c.submit = submit

	started, startErr := c.process.Start(func(runCtx context.Context) (err error) {
		defer func() {
			c.mu.Lock()
			if c.done == done {
				c.running = false
				c.done = nil
				c.submit = nil
				if err != nil && !stderrors.Is(err, context.Canceled) {
					c.lastErr = err
				}
			}
			c.mu.Unlock()

			close(done)
			if err != nil && !stderrors.Is(err, context.Canceled) && c.notify != nil {
				c.notify(context.Background(), fmt.Sprintf("监听下载已停止：%v\n请检查配置或重新登录。", err))
			}
		}()
		err = runControllerWatch(runCtx, opts)
		return err
	}, rte.Recovery{})
	c.mu.Unlock()
	if !started {
		c.mu.Lock()
		c.running = false
		c.lastErr = startErr
		c.submit = nil
		c.done = nil
		c.mu.Unlock()
	}
	return started
}

func (c *Controller) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), controllerStopTimeout)
	defer cancel()
	_ = c.StopContext(ctx)
}

func (c *Controller) StopContext(ctx context.Context) error {
	c.mu.Lock()
	process := c.process
	c.mu.Unlock()
	return process.Stop(ctx)
}
func (c *Controller) Health() rte.Health { return c.process.Health() }

func (c *Controller) UpdateOptions(opts Options) {
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
	if c.lastErr != nil {
		return c.lastErr
	}
	return c.process.LastError()
}

func (c *Controller) SubmitMessageLink(ctx context.Context, link string) (MessageLinkSubmissionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	c.mu.Lock()
	opts := c.opts
	c.mu.Unlock()
	if opts.FeatureFlags != nil {
		download, _ := opts.FeatureFlags()
		if !download {
			return MessageLinkSubmissionResult{}, stderrors.New("监听下载已停用")
		}
	}
	link, err := validateMessageLink(ctx, opts, link)
	if err != nil {
		return MessageLinkSubmissionResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	c.mu.Lock()
	submit := c.submit
	done := c.done
	running := c.running
	c.mu.Unlock()
	if !running || submit == nil || done == nil {
		return MessageLinkSubmissionResult{}, stderrors.New("监听下载未运行")
	}

	req := messageLinkSubmission{
		ctx:   ctx,
		link:  link,
		reply: make(chan messageLinkSubmissionResponse, 1),
	}
	select {
	case submit <- req:
	case <-done:
		return MessageLinkSubmissionResult{}, stderrors.New("监听下载已停止")
	case <-ctx.Done():
		return MessageLinkSubmissionResult{}, ctx.Err()
	}

	select {
	case resp := <-req.reply:
		return resp.result, resp.err
	case <-done:
		return MessageLinkSubmissionResult{}, stderrors.New("监听下载已停止")
	case <-ctx.Done():
		return MessageLinkSubmissionResult{}, ctx.Err()
	}
}

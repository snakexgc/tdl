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
	parent context.Context
	opts   Options
	notify NotifyFunc

	mu       sync.Mutex
	cancel   context.CancelFunc
	done     chan struct{}
	running  bool
	lastErr  error
	submit   chan messageLinkSubmission
	policies *rte.Runtime
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
	submit := make(chan messageLinkSubmission, 100)
	opts := c.opts
	opts.Notify = c.notify
	opts.messageLinks = submit
	var policies *rte.Runtime
	if (opts.Filter == nil) != (opts.Naming == nil) {
		cancel()
		c.lastErr = fmt.Errorf("filter and naming ports must be injected together")
		c.mu.Unlock()
		return false
	}
	if opts.Filter == nil {
		var err error
		policies, opts.Filter, opts.Naming, err = startPolicies(ctx, string(opts.Account), opts)
		if err != nil {
			cancel()
			c.lastErr = err
			c.mu.Unlock()
			return false
		}
	}
	c.policies = policies
	c.running = true
	c.cancel = cancel
	c.done = done
	c.lastErr = nil
	c.submit = submit
	c.mu.Unlock()

	go func() {
		err := runControllerWatch(ctx, opts)
		cancel()
		if policies != nil {
			_ = policies.Stop(context.Background())
		}

		if err != nil && !stderrors.Is(err, context.Canceled) && c.notify != nil {
			c.notify(context.Background(), fmt.Sprintf("监听下载已停止：%v\n请检查配置或重新登录。", err))
		}

		c.mu.Lock()
		if c.done == done {
			c.running = false
			c.cancel = nil
			c.done = nil
			c.submit = nil
			c.policies = nil
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
	if c.policies != nil {
		if err := c.policies.ReconfigureBatch(c.parent, map[string]map[string]any{
			filterComponentID: filterConfig(opts),
			namingComponentID: namingConfig(opts),
		}); err != nil {
			c.lastErr = fmt.Errorf("reconfigure policies: %w", err)
			return
		}
		c.lastErr = nil
	}
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

func (c *Controller) SubmitMessageLink(ctx context.Context, link string) (MessageLinkSubmissionResult, error) {
	link, err := ValidateTelegramMessageHTTPLink(link)
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

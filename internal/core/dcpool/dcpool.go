package dcpool

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"go.uber.org/multierr"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/internal/core/logctx"
	"github.com/snakexgc/tdl/internal/core/middlewares/takeout"
)

var testMode = false

var takeoutInit = takeout.Takeout

// EnableTestMode enables test mode, which disables takeout and pooling and directly returns original client.
func EnableTestMode() {
	testMode = true
}

type Pool interface {
	Client(ctx context.Context, dc int) *tg.Client
	Takeout(ctx context.Context, dc int) *tg.Client
	Default(ctx context.Context) *tg.Client
	Close() error
}

type pool struct {
	api         *telegram.Client
	size        int64
	mu          *sync.Mutex
	middlewares []telegram.Middleware

	invokers  map[int]tg.Invoker
	closes    map[int]func() error
	takeout   int64
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

func NewPool(c *telegram.Client, size int64, middlewares ...telegram.Middleware) Pool {
	return &pool{
		api:         c,
		size:        size,
		mu:          &sync.Mutex{},
		middlewares: middlewares,
		invokers:    make(map[int]tg.Invoker),
		closes:      make(map[int]func() error),
		takeout:     0,
	}
}

func (p *pool) current() int {
	return p.api.Config().ThisDC
}

func (p *pool) Client(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	defer p.mu.Unlock()

	return tg.NewClient(p.invoker(ctx, dc))
}

func (p *pool) invoker(ctx context.Context, dc int) tg.Invoker {
	if p.closed {
		return closedInvoker{}
	}
	// self-hosted Telegram server can't properly handle pooling connections,
	// so directly return original client
	if testMode {
		return p.api
	}

	if i, ok := p.invokers[dc]; ok {
		return i
	}

	// lazy init
	var (
		invoker telegram.CloseInvoker
		err     error
	)
	if dc == p.current() { // can't transfer dc to current dc
		invoker, err = p.api.Pool(p.size)
	} else {
		invoker, err = p.api.DC(ctx, dc, p.size)
	}

	if err != nil {
		logctx.From(ctx).Error("create invoker", zap.Error(err))
		return p.api // degraded
	}

	p.closes[dc] = invoker.Close
	p.invokers[dc] = chainMiddlewares(invoker, p.middlewares...)

	return p.invokers[dc]
}

func (p *pool) Default(ctx context.Context) *tg.Client {
	return p.Client(ctx, p.current())
}

func (p *pool) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		sid := p.takeout
		closes := p.closes
		p.closes = nil
		p.invokers = nil
		p.mu.Unlock()

		// Finish through the existing connection; never initialize a pool while
		// shutting down, and bound the network request independently of callers.
		if sid != 0 {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			p.closeErr = takeout.UnTakeout(ctx, chainMiddlewares(p.api, takeout.Middleware(sid)))
			cancel()
		}
		for _, closeInvoker := range closes {
			p.closeErr = multierr.Append(p.closeErr, closeInvoker())
		}
	})
	return p.closeErr
}

var ErrClosed = errors.New("DC pool is closed")

type closedInvoker struct{}

func (closedInvoker) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	return ErrClosed
}

func (p *pool) Takeout(ctx context.Context, dc int) *tg.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return tg.NewClient(closedInvoker{})
	}

	// lazy init
	if p.takeout == 0 {
		sid, err := takeoutInit(ctx, p.api)
		if err != nil {
			logctx.From(ctx).Warn("takeout error", zap.Error(err))
			// ignore init delay error and return non-takeout client
			return tg.NewClient(p.invoker(ctx, dc))
		}
		p.takeout = sid
		logctx.From(ctx).Info("get takeout id", zap.Int64("id", sid))
	}

	return tg.NewClient(chainMiddlewares(p.invoker(ctx, dc), takeout.Middleware(p.takeout)))
}

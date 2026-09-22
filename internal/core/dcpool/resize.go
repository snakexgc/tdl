package dcpool

import (
	"context"
	"errors"
	"sync"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

// NewResizable keeps stable client handles across connection-capacity changes.
// Each in-flight RPC leases its old generation; new RPCs use the new capacity.
// Retired generations close only after their last RPC completes.
func NewResizable(client *telegram.Client, size int64, middleware ...telegram.Middleware) Pool {
	return newResizable(size, func(size int64) Pool { return NewPool(client, size, middleware...) })
}

type poolGeneration struct {
	pool Pool
	refs int
}

type resizablePool struct {
	mu        sync.Mutex
	size      int64
	current   *poolGeneration
	retired   map[*poolGeneration]bool
	factory   func(int64) Pool
	closed    bool
	closeOnce sync.Once
	closing   sync.WaitGroup
	closeErr  error
}

func newResizable(size int64, factory func(int64) Pool) *resizablePool {
	return &resizablePool{size: size, current: &poolGeneration{pool: factory(size)}, retired: map[*poolGeneration]bool{}, factory: factory}
}

func Resize(pool Pool, size int64) {
	if resizable, ok := pool.(interface{ Resize(int64) }); ok {
		resizable.Resize(size)
	}
}

func (p *resizablePool) Resize(size int64) {
	p.mu.Lock()
	if p.closed || size < 1 || p.size == size {
		p.mu.Unlock()
		return
	}
	old := p.current
	p.current, p.size = &poolGeneration{pool: p.factory(size)}, size
	if old.refs > 0 {
		p.retired[old] = true
		p.mu.Unlock()
		return
	}
	p.closing.Add(1)
	p.mu.Unlock()
	p.recordClose(old.pool.Close())
	p.closing.Done()
}

func (p *resizablePool) recordClose(err error) {
	p.mu.Lock()
	p.closeErr = errors.Join(p.closeErr, err)
	p.mu.Unlock()
}

func (p *resizablePool) Client(_ context.Context, dc int) *tg.Client {
	return tg.NewClient(resizingInvoker{pool: p, dc: dc})
}

func (p *resizablePool) Default(_ context.Context) *tg.Client {
	return tg.NewClient(resizingInvoker{pool: p, defaultDC: true})
}

func (p *resizablePool) Takeout(_ context.Context, dc int) *tg.Client {
	return tg.NewClient(resizingInvoker{pool: p, dc: dc, takeout: true})
}

func (p *resizablePool) Close() error {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		p.closed = true
		all := []*poolGeneration{p.current}
		for generation := range p.retired {
			all = append(all, generation)
		}
		p.retired = nil
		p.mu.Unlock()
		for _, generation := range all {
			p.recordClose(generation.pool.Close())
		}
		p.closing.Wait()
	})
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeErr
}

type resizingInvoker struct {
	pool               *resizablePool
	dc                 int
	defaultDC, takeout bool
}

func (i resizingInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	p := i.pool
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	generation := p.current
	generation.refs++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		generation.refs--
		closeOld := generation.refs == 0 && p.retired[generation]
		if closeOld {
			delete(p.retired, generation)
			p.closing.Add(1)
		}
		p.mu.Unlock()
		if closeOld {
			p.recordClose(generation.pool.Close())
			p.closing.Done()
		}
	}()
	var client *tg.Client
	switch {
	case i.defaultDC:
		client = generation.pool.Default(ctx)
	case i.takeout:
		client = generation.pool.Takeout(ctx, i.dc)
	default:
		client = generation.pool.Client(ctx, i.dc)
	}
	return client.Invoker().Invoke(ctx, input, output)
}

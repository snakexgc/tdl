package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/schedule"
)

const ID = "proxy.range"

type lifetime struct {
	runnables *schedule.Group
	mu        sync.Mutex
	ctx       context.Context
	closed    bool
	active    sync.WaitGroup
}

func Register(registry *rte.Registry, handler *Handler) error {
	if handler == nil || handler.source == nil {
		return errors.New("range proxy requires a source")
	}
	return registry.Register(Manifest(), func() rte.Component { return handler })
}

func (h *Handler) Init(ctx context.Context, k rte.Kernel) error {
	h.life.ctx = ctx
	h.life.runnables = k.Runnables
	if err := h.Reconfigure(ctx, k.Config); err != nil {
		return err
	}
	return k.Provide(ports.RangeHandlerName, h)
}

func (h *Handler) Start(context.Context) error {
	maintenance, ok := h.source.(ports.RangeMaintenance)
	if !ok {
		return nil
	}
	// Initial configuration is already loaded; reserve change notifications for
	// later patches so the first expired-task cleanup runs immediately.
	select {
	case <-h.taskChanged:
	default:
	}
	select {
	case <-h.sourceChanged:
	default:
	}
	if err := h.life.runnables.RunDynamic("tasks.expire", 0, func() time.Duration { return h.policy().tasks }, h.taskChanged, maintenance.CleanupExpired, nil); err != nil {
		return err
	}
	return h.life.runnables.RunDynamic("sources.expire", h.policy().sources, func() time.Duration { return h.policy().sources }, h.sourceChanged, maintenance.CleanupSources, nil)
}

func (h *Handler) Stop(ctx context.Context) error {
	h.life.mu.Lock()
	h.life.closed = true
	h.life.mu.Unlock()
	done := make(chan struct{})
	go func() { h.life.active.Wait(); close(done) }()
	select {
	case <-done:
		if maintenance, ok := h.source.(ports.RangeMaintenance); ok {
			return maintenance.CleanupSources(ctx)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.life.mu.Lock()
	if h.life.closed || h.life.ctx.Err() != nil {
		h.life.mu.Unlock()
		http.Error(w, "range proxy is stopped", http.StatusServiceUnavailable)
		return
	}
	h.life.active.Add(1)
	h.life.mu.Unlock()
	defer h.life.active.Done()
	ctx, cancel := context.WithCancel(r.Context())
	unlink := context.AfterFunc(h.life.ctx, cancel)
	defer func() { unlink(); cancel() }()
	h.serveHTTP(w, r.WithContext(ctx))
}

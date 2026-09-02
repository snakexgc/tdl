package httpdl

import (
	"context"
	stderrors "errors"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

const controllerStopTimeout = 10 * time.Second

// Service contains the state shared by the standalone HTTP server and the
// download automation that creates links served by it. Sharing this state does
// not tie their lifecycles together: Controller can be stopped while tasks are
// still created, and can later be started again with the same task registry.
type Service struct {
	proxy *Proxy
	pools *PoolHolder
}

func NewService(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Service {
	if cfg == nil {
		cfg = config.Get()
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	pools := &PoolHolder{}
	proxy := NewProxy(cfg.HTTP, config.EffectiveLimit(cfg), config.EffectivePoolSize(cfg), pools, kvd, logger)
	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		proxy.SetTaskTTL(0)
	}
	return &Service{proxy: proxy, pools: pools}
}

func (s *Service) Proxy() *Proxy {
	if s == nil {
		return nil
	}
	return s.proxy
}

func (s *Service) Pools() *PoolHolder {
	if s == nil {
		return nil
	}
	return s.pools
}

// UpdateConfig refreshes link generation and expiry without rebuilding the
// shared task state. The return value reports whether the listening address
// changed and the standalone HTTP controller therefore needs a restart.
func (s *Service) UpdateConfig(cfg *config.Config) bool {
	if s == nil || s.proxy == nil || cfg == nil {
		return false
	}
	restart := s.proxy.updateConfig(cfg.HTTP)
	if config.EffectiveDownloaderMode(cfg) == config.DownloaderModeInternal {
		s.proxy.SetTaskTTL(0)
	} else {
		s.proxy.SetTaskTTL(LinkTTL(cfg.HTTP))
	}
	return restart
}

// Controller owns only the HTTP listening lifecycle. Telegram connectivity,
// aria2 RPC automation and task submission are deliberately managed elsewhere.
type Controller struct {
	parent  context.Context
	service *Service

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
	lastErr error
}

func NewController(parent context.Context, service *Service) *Controller {
	if parent == nil {
		parent = context.Background()
	}
	return &Controller{parent: parent, service: service}
}

func (c *Controller) Start() bool {
	if c == nil || c.service == nil || c.service.Proxy() == nil {
		return false
	}

	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(c.parent)
	done := make(chan struct{})
	c.cancel = cancel
	c.done = done
	c.running = true
	c.lastErr = nil
	c.mu.Unlock()

	go func() {
		err := c.service.Proxy().Start(ctx)
		cancel()

		c.mu.Lock()
		if c.done == done {
			c.cancel = nil
			c.done = nil
			c.running = false
			if err != nil && !stderrors.Is(err, http.ErrServerClosed) && !stderrors.Is(err, context.Canceled) {
				c.lastErr = err
			}
		}
		c.mu.Unlock()
		close(done)
	}()

	return true
}

func (c *Controller) Stop() {
	if c == nil {
		return
	}
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

func (c *Controller) Running() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.running
}

func (c *Controller) LastError() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastErr
}

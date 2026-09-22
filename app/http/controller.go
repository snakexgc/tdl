package httpdl

import (
	"context"
	stderrors "errors"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
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
	proxy := NewProxy(cfg.HTTP, cfg.Limit, cfg.PoolSize, pools, kvd, logger)
	proxy.account = types.AccountID(cfg.Namespace)
	if config.UsesDownloadExecutor(cfg, config.DownloadExecutorLocal) {
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
	s.proxy.Scheduler().Reconfigure(cfg.Limit, cfg.PoolSize)
	s.pools.Resize(int64(cfg.PoolSize))
	if config.UsesDownloadExecutor(cfg, config.DownloadExecutorLocal) {
		s.proxy.SetTaskTTL(0)
	} else {
		s.proxy.SetTaskTTL(LinkTTL(cfg.HTTP))
	}
	return restart
}

// Controller owns only the HTTP listening lifecycle. Telegram connectivity,
// aria2 RPC automation and task submission are deliberately managed elsewhere.
type Controller struct {
	service *Service
	process *rte.Process
	startMu sync.Mutex
	ready   atomic.Bool
	lastErr error
}

func NewController(parent context.Context, service *Service) *Controller {
	account := types.DefaultAccount
	if service != nil && service.proxy != nil {
		account = service.proxy.account
	}
	return &Controller{service: service, process: rte.NewProcess(parent, account, "host.http")}
}

func (c *Controller) Start() bool {
	if c == nil || c.service == nil || c.service.Proxy() == nil {
		return false
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.Running() {
		return false
	}
	c.lastErr = nil
	ready, finished := make(chan struct{}), make(chan error, 1)
	started, err := c.process.Start(func(ctx context.Context) (err error) {
		defer func() { c.ready.Store(false); finished <- err }()
		err = c.service.Proxy().start(ctx, func() { c.ready.Store(true); close(ready) })
		if stderrors.Is(err, http.ErrServerClosed) || stderrors.Is(err, context.Canceled) {
			return nil
		}
		if err != nil {
			c.service.Proxy().logger.Error("HTTP 下载服务启动或运行失败", zap.String("listen", config.HTTPConfigListenAddr(c.service.Proxy().config())), zap.Error(err))
		}
		return err
	}, rte.Recovery{})
	if !started {
		c.lastErr = err
		return false
	}
	select {
	case <-ready:
		return true
	case err := <-finished:
		c.lastErr = err
		// Drain the failed invocation so a corrected configuration can restart immediately.
		ctx, cancel := context.WithTimeout(context.Background(), controllerStopTimeout)
		defer cancel()
		_ = c.process.Stop(ctx)
		return false
	}
}

func (c *Controller) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), controllerStopTimeout)
	defer cancel()
	_ = c.StopContext(ctx)
}

func (c *Controller) StopContext(ctx context.Context) error {
	if c == nil {
		return nil
	}
	return c.process.Stop(ctx)
}
func (c *Controller) Running() bool { return c != nil && c.ready.Load() && c.process.Running() }
func (c *Controller) LastError() error {
	if c == nil {
		return nil
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.lastErr != nil {
		return c.lastErr
	}
	return c.process.LastError()
}
func (c *Controller) Health() rte.Health { return c.process.Health() }

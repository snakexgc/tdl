package aria2

import (
	"context"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	appdownload "github.com/snakexgc/tdl/app/download"
	"github.com/snakexgc/tdl/core/storage"
	"github.com/snakexgc/tdl/pkg/config"
)

// Manager owns aria2 connectivity, recovery and monitoring. It is deliberately
// independent from the Telegram watcher and HTTP server lifecycles.
type Manager struct {
	controller *Controller
	client     *Client
	store      *TaskStore
	regulator  *TelegramErrorRegulator
	monitor    *ZeroSpeedMonitor
	limit      int
	baseURL    string
	logger     *zap.Logger
}

func NewManager(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Manager {
	if cfg == nil {
		cfg = config.Get()
	}
	if cfg == nil {
		cfg = config.DefaultConfig()
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.Named("aria2-manager")
	client := NewClient(cfg.Aria2)
	store := NewTaskStore(kvd, downloadLinkTTL(cfg.HTTP))
	controller := &Controller{
		client:        client,
		store:         store,
		publicBaseURL: cfg.HTTP.PublicBaseURL,
		connections:   config.EffectivePoolSize(cfg),
		logger:        logger,
	}
	return &Manager{
		controller: controller,
		client:     client,
		store:      store,
		regulator:  NewTelegramErrorRegulator(client, store, cfg.HTTP.PublicBaseURL, logger),
		monitor:    NewZeroSpeedMonitor(client, store, cfg.HTTP.PublicBaseURL, logger),
		limit:      config.EffectiveLimit(cfg),
		baseURL:    cfg.HTTP.PublicBaseURL,
		logger:     logger,
	}
}

func (m *Manager) Name() string {
	return aria2DownloaderName
}

func (m *Manager) Submit(ctx context.Context, submission appdownload.Submission) (appdownload.Result, error) {
	if m == nil || m.controller == nil {
		return appdownload.Result{}, errors.New("aria2 manager is not initialized")
	}
	return m.controller.Submit(ctx, submission)
}

// ReportTelegramFileError lets the optional manager react to Telegram stream
// errors without making the HTTP package depend on aria2.
func (m *Manager) ReportTelegramFileError(ctx context.Context, err error) {
	if m == nil || m.regulator == nil {
		return
	}
	m.regulator.ReportTelegramFileError(ctx, err)
}

func (m *Manager) Run(ctx context.Context) error {
	if m == nil || m.client == nil {
		return errors.New("aria2 manager is not initialized")
	}
	if err := m.waitUntilReady(ctx, DefaultConnectRetryInterval); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	m.logger.Info("Aria2 manager connected", zap.Int("max_concurrent_downloads", m.limit))
	go m.regulator.Run(ctx)
	go m.monitor.Run(ctx)
	if count, err := ResumeStartupPausedTasks(ctx, m.client, m.store, m.baseURL, m.logger); err != nil {
		if !errors.Is(err, context.Canceled) {
			m.logger.Warn("Failed to resume paused aria2 tasks at startup", zap.Error(err))
		}
	} else if count > 0 {
		m.logger.Info("Resumed paused aria2 tasks at startup", zap.Int("count", count))
	}

	<-ctx.Done()
	if paused, err := PauseTDLTasksForShutdown(ctx, m.client, m.store, m.baseURL, m.logger); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			m.logger.Warn("Failed to pause aria2 tasks during manager shutdown", zap.Error(err))
		}
	} else if len(paused) > 0 {
		m.logger.Info("Paused aria2 tasks during manager shutdown", zap.Int("count", len(paused)))
	}
	return nil
}

func (m *Manager) waitUntilReady(ctx context.Context, retryInterval time.Duration) error {
	if retryInterval <= 0 {
		retryInterval = DefaultConnectRetryInterval
	}
	delay := min(retryInterval, maxConnectRetryInterval)
	for {
		err := m.client.SetMaxConcurrentDownloads(ctx, m.limit)
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		m.logger.Warn("Aria2 manager is not ready, retrying",
			zap.Duration("retry_interval", delay),
			zap.Error(err))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
		delay = nextAria2RetryInterval(delay)
	}
}

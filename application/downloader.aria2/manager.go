package aria2

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte/schedule"
)

// Manager owns aria2 connectivity, recovery and monitoring. It is deliberately
// independent from the Telegram watcher and HTTP server lifecycles.
type Manager struct {
	observer      *Observer
	configuration atomic.Pointer[governancePolicy]
	statusChanged chan struct{}
	retryChanged  chan struct{}
	controller    *Controller
	client        ports.Aria2Client
	store         ports.Aria2Repository
	regulator     *TelegramErrorRegulator
	monitor       *ZeroSpeedMonitor
	limit         int
	baseURL       string
	logger        *zap.Logger
}

func NewManager(opts Options, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	m := &Manager{
		statusChanged: make(chan struct{}, 1), retryChanged: make(chan struct{}, 1),
		controller: NewController(opts, logger), client: opts.Client, store: opts.Store,
		regulator: NewTelegramErrorRegulator(opts.Client, opts.Store, opts.PublicBaseURL, logger),
		monitor:   NewZeroSpeedMonitor(opts.Client, opts.Store, opts.PublicBaseURL, logger),
		limit:     opts.Limit, baseURL: opts.PublicBaseURL, logger: logger,
	}
	if opts.Observations != nil {
		m.observer = &Observer{Client: opts.Client, Repository: opts.Observations, PublicBaseURL: opts.PublicBaseURL, TTL: opts.LinkTTL}
	}
	m.monitor.configuration = func() zeroSpeedMonitorConfig { return m.policy().monitor }
	m.regulator.configuration = func() telegramErrorRegulatorConfig { return m.policy().regulator }
	return m
}

func (m *Manager) Name() string {
	return aria2DownloaderName
}

func (m *Manager) Submit(ctx context.Context, submission types.DownloadSubmission) (types.DownloadResult, error) {
	if m == nil || m.controller == nil {
		return types.DownloadResult{}, fmt.Errorf("aria2 manager is not initialized: %w", ports.ErrDownloadNotAccepted)
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
	if err := m.waitUntilReady(ctx, 0); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	m.logger.Info("Aria2 manager connected", zap.Int("max_concurrent_downloads", m.limit))
	governors := schedule.New(ctx)
	defer func() {
		// The owning RTE enforces the caller's stop deadline. Keep this
		// Runnable alive until its children actually release their resources.
		_ = governors.Stop(context.Background())
	}()
	for name, run := range map[string]func(context.Context){"telegram-errors": m.regulator.Run, "zero-speed": m.monitor.Run} {
		if err := governors.Run(name, 0, 0, func(ctx context.Context) error { run(ctx); return nil }, func(err error) { m.logger.Error("Aria2 governor failed", zap.String("governor", name), zap.Error(err)) }); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	if count, err := ResumeStartupPausedTasks(ctx, m.client, m.store, m.baseURL, m.logger); err != nil {
		if !errors.Is(err, context.Canceled) {
			m.logger.Warn("Failed to resume paused aria2 tasks at startup", zap.Error(err))
		}
	} else if count > 0 {
		m.logger.Info("Resumed paused aria2 tasks at startup", zap.Int("count", count))
	}

	<-ctx.Done()
	if err := governors.Stop(context.Background()); err != nil {
		return errors.Wrap(err, "stop aria2 governors")
	}
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
	select {
	case <-m.retryChanged:
	default:
	}
	dynamic := retryInterval <= 0
	if retryInterval <= 0 {
		retryInterval = m.policy().retry
	}
	delay := min(retryInterval, m.policy().maximum)
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
		case <-m.retryChanged:
			timer.Stop()
			if dynamic {
				delay = m.policy().retry
			}
			continue
		}
		delay = min(delay*2, m.policy().maximum)
	}
}

func (m *Manager) syncStates(ctx context.Context) error {
	if m.observer != nil {
		return m.observer.Sync(ctx)
	}
	return m.controller.SyncStates(ctx)
}

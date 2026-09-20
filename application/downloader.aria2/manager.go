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
	automatic     automaticClient
	store         ports.Aria2Repository
	regulator     *TelegramErrorRegulator
	monitor       *ZeroSpeedMonitor
	limit         int
	transferLimit atomic.Int64
	limitChanged  chan struct{}
	baseURL       string
	links         atomic.Pointer[linkPolicy]
	logger        *zap.Logger
}

func NewManager(opts Options, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("component", "downloader.aria2"))
	m := &Manager{
		statusChanged: make(chan struct{}, 1), retryChanged: make(chan struct{}, 1),
		limitChanged: make(chan struct{}, 1),
		controller:   NewController(opts, logger), client: opts.Client, store: opts.Store,
		limit: opts.Limit, baseURL: opts.PublicBaseURL, logger: logger,
	}
	m.automatic = automaticClient{Aria2Client: opts.Client, controller: m.controller, owner: "lifecycle", resumeAny: true}
	m.regulator = NewTelegramErrorRegulator(automaticClient{Aria2Client: opts.Client, controller: m.controller, owner: "telegram-errors"}, opts.Store, opts.PublicBaseURL, logger)
	m.monitor = NewZeroSpeedMonitor(automaticClient{Aria2Client: opts.Client, controller: m.controller, owner: "zero-speed"}, opts.Store, opts.PublicBaseURL, logger)
	if opts.Observations != nil {
		m.observer = &Observer{Logger: logger, Client: opts.Client, Repository: opts.Observations, PublicBaseURL: opts.PublicBaseURL, TTL: opts.LinkTTL, links: &m.links}
	}
	m.UpdateLinkPolicy(opts.PublicBaseURL, opts.LinkTTL)
	m.controller.links, m.monitor.links, m.regulator.links = &m.links, &m.links, &m.links
	m.monitor.configuration = func() zeroSpeedMonitorConfig { return m.policy().monitor }
	m.regulator.configuration = func() telegramErrorRegulatorConfig { return m.policy().regulator }
	return m
}

func (m *Manager) Name() string {
	return aria2DownloaderName
}

// UpdateTransferLimits changes scheduling without pausing existing aria2 jobs.
// RPC failures are retried by the manager while transfer workers keep running.
func (m *Manager) UpdateTransferLimits(files, connections int) {
	m.controller.connectionLimit.Store(int64(max(1, connections)))
	if m.transferLimit.Swap(int64(max(1, files))) != int64(max(1, files)) {
		select {
		case m.limitChanged <- struct{}{}:
		default:
		}
	}
}

func (m *Manager) fileLimit() int {
	if limit := m.transferLimit.Load(); limit > 0 {
		return int(limit)
	}
	return m.limit
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
	if m == nil || m.client == nil || m.store == nil {
		return errors.New("aria2 manager is not initialized")
	}
	if err := m.waitUntilReady(ctx, 0); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}

	m.logger.Info("Aria2 manager connected", zap.Int("max_concurrent_downloads", m.fileLimit()))
	governors := schedule.New(ctx)
	defer func() {
		// The owning RTE enforces the caller's stop deadline. Keep this
		// Runnable alive until its children actually release their resources.
		_ = governors.Stop(context.Background())
	}()
	if count, err := ResumeStartupPausedTasks(ctx, m.automatic, m.store, currentBaseURL(&m.links, m.baseURL), m.logger); err != nil {
		if !errors.Is(err, context.Canceled) {
			m.logger.Warn("Failed to resume paused aria2 tasks at startup", zap.Error(err))
		}
	} else if count > 0 {
		m.logger.Info("Resumed paused aria2 tasks at startup", zap.Int("count", count))
	}

	for name, run := range map[string]func(context.Context){"telegram-errors": m.regulator.Run, "zero-speed": m.monitor.Run} {
		if err := governors.Run(name, 0, 0, func(ctx context.Context) error { run(ctx); return nil }, func(err error) { m.logger.Error("Aria2 governor failed", zap.String("governor", name), zap.Error(err)) }); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}

	if err := governors.Run("transfer-limits", 0, 0, m.applyTransferLimits, nil); err != nil {
		if ctx.Err() == nil {
			return err
		}
		// Cancellation may arrive during startup recovery. Continue through
		// the normal drain/pause path instead of reporting a scheduling failure.
	}
	<-ctx.Done()
	if err := governors.Stop(context.Background()); err != nil {
		return errors.Wrap(err, "stop aria2 governors")
	}
	if paused, err := PauseTDLTasksForShutdown(ctx, m.automatic, m.store, currentBaseURL(&m.links, m.baseURL), m.logger); err != nil {
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
	attempt := 0
	for {
		err := m.client.SetMaxConcurrentDownloads(ctx, m.fileLimit())
		if err == nil {
			if attempt > 0 {
				m.logger.Info("Aria2 connection recovered", zap.Int("attempts", attempt))
			}
			return nil
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		attempt++
		level := zap.DebugLevel
		if attempt == 1 {
			level = zap.WarnLevel
		}
		m.logger.Log(level, "Aria2 manager is not ready, retrying",
			zap.Int("attempt", attempt),
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

func (m *Manager) applyTransferLimits(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.limitChanged:
		}
		attempt := 0
		for ctx.Err() == nil {
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := m.client.SetMaxConcurrentDownloads(bounded, m.fileLimit())
			cancel()
			if err == nil {
				m.logger.Info("Aria2 transfer limit applied", zap.Int("max_concurrent_downloads", m.fileLimit()), zap.Int("retries", attempt))
				break
			}
			if ctx.Err() != nil {
				return nil
			}
			attempt++
			level := zap.DebugLevel
			if attempt == 1 {
				level = zap.WarnLevel
			}
			m.logger.Log(level, "Cannot apply aria2 transfer limit; retrying", zap.Int("attempt", attempt), zap.Error(err))
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
			case <-m.limitChanged:
				timer.Stop()
			}
		}
	}
}

func (m *Manager) syncStates(ctx context.Context) error {
	if m.observer != nil {
		return m.observer.Sync(ctx)
	}
	return m.controller.SyncStates(ctx)
}

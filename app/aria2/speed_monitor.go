package aria2

import (
	"context"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"
)

const (
	defaultZeroSpeedMonitorPollInterval   = 30 * time.Second
	defaultZeroSpeedMonitorStallThreshold = 3 * time.Minute
	defaultZeroSpeedMonitorPauseDuration  = 10 * time.Second
	defaultZeroSpeedMonitorActionTimeout  = 30 * time.Second
)

// ZeroSpeedMonitorClient is the subset of the aria2 client used by ZeroSpeedMonitor.
type ZeroSpeedMonitorClient interface {
	TellActive(ctx context.Context) ([]DownloadStatus, error)
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
}

type zeroSpeedMonitorConfig struct {
	PollInterval   time.Duration
	StallThreshold time.Duration
	PauseDuration  time.Duration
	ActionTimeout  time.Duration
}

// ZeroSpeedMonitor watches active tdl-owned aria2 tasks and cycles pause/resume
// for any task whose download speed stays at zero beyond the stall threshold.
type ZeroSpeedMonitor struct {
	client        ZeroSpeedMonitorClient
	store         *TaskStore
	publicBaseURL string
	logger        *zap.Logger
	cfg           zeroSpeedMonitorConfig
	now           func() time.Time

	mu    sync.Mutex
	stall map[string]time.Time // GID -> time zero speed was first observed
}

// NewZeroSpeedMonitor creates a ZeroSpeedMonitor with default configuration.
func NewZeroSpeedMonitor(client ZeroSpeedMonitorClient, store *TaskStore, publicBaseURL string, logger *zap.Logger) *ZeroSpeedMonitor {
	return newZeroSpeedMonitor(client, store, publicBaseURL, logger, zeroSpeedMonitorConfig{})
}

func newZeroSpeedMonitor(client ZeroSpeedMonitorClient, store *TaskStore, publicBaseURL string, logger *zap.Logger, cfg zeroSpeedMonitorConfig) *ZeroSpeedMonitor {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeZeroSpeedMonitorConfig(cfg)
	return &ZeroSpeedMonitor{
		client:        client,
		store:         store,
		publicBaseURL: publicBaseURL,
		logger:        logger.Named("aria2-speed-monitor"),
		cfg:           cfg,
		now:           time.Now,
		stall:         make(map[string]time.Time),
	}
}

func normalizeZeroSpeedMonitorConfig(cfg zeroSpeedMonitorConfig) zeroSpeedMonitorConfig {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultZeroSpeedMonitorPollInterval
	}
	if cfg.StallThreshold <= 0 {
		cfg.StallThreshold = defaultZeroSpeedMonitorStallThreshold
	}
	if cfg.PauseDuration <= 0 {
		cfg.PauseDuration = defaultZeroSpeedMonitorPauseDuration
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = defaultZeroSpeedMonitorActionTimeout
	}
	return cfg
}

// Run polls aria2 on each tick and cycles pause/resume on stalled tasks.
// It returns when ctx is cancelled.
func (m *ZeroSpeedMonitor) Run(ctx context.Context) {
	if m == nil || m.client == nil || m.store == nil {
		return
	}

	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.poll(ctx); err != nil && !errors.Is(err, context.Canceled) {
				m.logger.Warn("Zero speed monitor poll failed",
					zap.Error(err))
			}
		}
	}
}

func (m *ZeroSpeedMonitor) poll(ctx context.Context) error {
	registeredGIDs, err := m.store.GIDs(ctx)
	if err != nil {
		return errors.Wrap(err, "load tdl aria2 task registry")
	}
	downloadPrefix, err := aria2DownloadURLPrefix(m.publicBaseURL)
	if err != nil {
		return err
	}

	actionCtx, cancel := context.WithTimeout(ctx, m.cfg.ActionTimeout)
	var active []DownloadStatus
	err = retryAria2ConnectionWithInterval(actionCtx, m.logger, "query aria2 active tasks for speed monitoring", time.Second, func() error {
		var qErr error
		active, qErr = m.client.TellActive(actionCtx)
		return qErr
	})
	cancel()
	if err != nil {
		return errors.Wrap(err, "query aria2 active tasks")
	}

	now := m.now()

	activeOwned := make(map[string]DownloadStatus, len(active))
	for _, task := range active {
		if normalizedAria2Status(task.Status) != aria2StatusActive {
			continue
		}
		if !isTDLAria2Task(task, registeredGIDs, downloadPrefix) {
			continue
		}
		activeOwned[task.GID] = task
	}

	m.mu.Lock()
	// Evict stall entries for tasks that are no longer active.
	for gid := range m.stall {
		if _, ok := activeOwned[gid]; !ok {
			delete(m.stall, gid)
		}
	}

	var stalled []string
	for gid, task := range activeOwned {
		if parseAria2Length(task.DownloadSpeed) > 0 {
			delete(m.stall, gid)
			continue
		}
		if _, exists := m.stall[gid]; !exists {
			m.stall[gid] = now
			continue
		}
		if now.Sub(m.stall[gid]) >= m.cfg.StallThreshold {
			stalled = append(stalled, gid)
			// Reset the clock so we don't re-trigger on every subsequent poll.
			m.stall[gid] = now
		}
	}
	m.mu.Unlock()

	for _, gid := range stalled {
		m.logger.Warn("Active aria2 task has had zero download speed beyond stall threshold; cycling pause/resume",
			zap.String("gid", gid),
			zap.Duration("stall_threshold", m.cfg.StallThreshold))
		if err := m.pauseAndResume(ctx, gid); err != nil && !errors.Is(err, context.Canceled) {
			m.logger.Warn("Failed to cycle pause/resume for stalled aria2 task",
				zap.String("gid", gid),
				zap.Error(err))
		}
	}

	return nil
}

func (m *ZeroSpeedMonitor) pauseAndResume(ctx context.Context, gid string) error {
	actionCtx, cancel := context.WithTimeout(ctx, m.cfg.ActionTimeout)
	err := retryAria2ConnectionWithInterval(actionCtx, m.logger, "force pause stalled aria2 task", time.Second, func() error {
		return m.client.ForcePause(actionCtx, gid)
	})
	cancel()
	if err != nil {
		return errors.Wrap(err, "pause stalled aria2 task")
	}
	m.logger.Info("Paused stalled aria2 task, waiting before resume",
		zap.String("gid", gid),
		zap.Duration("pause_duration", m.cfg.PauseDuration))

	timer := time.NewTimer(m.cfg.PauseDuration)
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

	actionCtx2, cancel2 := context.WithTimeout(ctx, m.cfg.ActionTimeout)
	err = retryAria2ConnectionWithInterval(actionCtx2, m.logger, "unpause stalled aria2 task", time.Second, func() error {
		return m.client.Unpause(actionCtx2, gid)
	})
	cancel2()
	if err != nil {
		return errors.Wrap(err, "resume stalled aria2 task")
	}
	m.logger.Info("Resumed stalled aria2 task after pause",
		zap.String("gid", gid))
	return nil
}

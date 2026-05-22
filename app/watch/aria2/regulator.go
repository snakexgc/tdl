package aria2

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"
)

const (
	defaultTelegramErrorRegulatorWindow        = 10 * time.Second
	defaultTelegramErrorRegulatorThreshold     = 3
	defaultTelegramErrorRegulatorCooldown      = 10 * time.Second
	defaultTelegramErrorRegulatorPauseDuration = 5 * time.Second
	defaultTelegramErrorRegulatorActionTimeout = 30 * time.Second
	defaultTelegramErrorRegulatorEventBuffer   = 64
)

type TelegramErrorRegulatorClient interface {
	TellActive(ctx context.Context) ([]DownloadStatus, error)
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
}

type telegramErrorRegulatorConfig struct {
	Window        time.Duration
	Threshold     int
	Cooldown      time.Duration
	PauseDuration time.Duration
	ActionTimeout time.Duration
	EventBuffer   int
}

type TelegramErrorRegulator struct {
	client        TelegramErrorRegulatorClient
	store         *TaskStore
	publicBaseURL string
	logger        *zap.Logger
	cfg           telegramErrorRegulatorConfig

	events chan error
	now    func() time.Time

	mu         sync.Mutex
	timestamps []time.Time
	lastAction time.Time
	regulating bool
}

func NewTelegramErrorRegulator(client TelegramErrorRegulatorClient, store *TaskStore, publicBaseURL string, logger *zap.Logger) *TelegramErrorRegulator {
	return newTelegramErrorRegulator(client, store, publicBaseURL, logger, telegramErrorRegulatorConfig{})
}

func newTelegramErrorRegulator(client TelegramErrorRegulatorClient, store *TaskStore, publicBaseURL string, logger *zap.Logger, cfg telegramErrorRegulatorConfig) *TelegramErrorRegulator {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg = normalizeTelegramErrorRegulatorConfig(cfg)
	return &TelegramErrorRegulator{
		client:        client,
		store:         store,
		publicBaseURL: publicBaseURL,
		logger:        logger.Named("aria2-regulator"),
		cfg:           cfg,
		events:        make(chan error, cfg.EventBuffer),
		now:           time.Now,
	}
}

func normalizeTelegramErrorRegulatorConfig(cfg telegramErrorRegulatorConfig) telegramErrorRegulatorConfig {
	if cfg.Window <= 0 {
		cfg.Window = defaultTelegramErrorRegulatorWindow
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = defaultTelegramErrorRegulatorThreshold
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = defaultTelegramErrorRegulatorCooldown
	}
	if cfg.PauseDuration <= 0 {
		cfg.PauseDuration = defaultTelegramErrorRegulatorPauseDuration
	}
	if cfg.ActionTimeout <= 0 {
		cfg.ActionTimeout = defaultTelegramErrorRegulatorActionTimeout
	}
	if cfg.EventBuffer <= 0 {
		cfg.EventBuffer = defaultTelegramErrorRegulatorEventBuffer
	}
	return cfg
}

func (r *TelegramErrorRegulator) ReportTelegramFileError(ctx context.Context, err error) {
	if r == nil || err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}

	select {
	case r.events <- err:
	default:
		r.logger.Debug("Dropping Telegram file error regulator event",
			zap.Error(err))
	}
}

func (r *TelegramErrorRegulator) Run(ctx context.Context) {
	if r == nil || r.client == nil || r.store == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case err := <-r.events:
			if !r.recordError(err) {
				continue
			}
			if regulateErr := r.regulate(ctx, err); regulateErr != nil && !errors.Is(regulateErr, context.Canceled) {
				r.logger.Warn("Failed to regulate aria2 tasks after Telegram file errors",
					zap.Error(regulateErr))
			}
			r.finishRegulation()
		}
	}
}

func (r *TelegramErrorRegulator) recordError(err error) bool {
	now := r.now()

	r.lock()
	defer r.unlock()

	cutoff := now.Add(-r.cfg.Window)
	next := r.timestamps[:0]
	for _, ts := range r.timestamps {
		if ts.After(cutoff) {
			next = append(next, ts)
		}
	}
	next = append(next, now)
	r.timestamps = next

	if len(r.timestamps) < r.cfg.Threshold {
		return false
	}
	if r.regulating {
		return false
	}
	if !r.lastAction.IsZero() && now.Sub(r.lastAction) < r.cfg.Cooldown {
		return false
	}

	r.regulating = true
	r.timestamps = nil
	r.logger.Warn("Frequent Telegram file errors detected; regulating aria2 tasks",
		zap.Int("error_count", len(next)),
		zap.Duration("window", r.cfg.Window),
		zap.Error(err))
	return true
}

func (r *TelegramErrorRegulator) finishRegulation() {
	r.lock()
	defer r.unlock()

	r.regulating = false
	r.lastAction = r.now()
	r.timestamps = nil
}

func (r *TelegramErrorRegulator) regulate(ctx context.Context, reason error) error {
	active, err := r.activeOwnedTasks(ctx)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		r.logger.Info("No active tdl aria2 tasks to regulate",
			zap.Error(reason))
		return nil
	}

	sortRegulationTasks(active)
	if len(active) > 1 {
		keep := active[0]
		toPause := active[1:]
		paused := r.forcePauseTasks(ctx, toPause, "pause extra aria2 task after Telegram file errors")
		r.logger.Warn("Paused extra aria2 tasks after frequent Telegram file errors",
			zap.String("kept_gid", keep.GID),
			zap.Int("active_tasks", len(active)),
			zap.Int("paused_tasks", len(paused)),
			zap.Duration("pause_duration", r.cfg.PauseDuration))
		return r.resumeAfterPause(ctx, paused)
	}

	task := active[0]
	paused := r.forcePauseTasks(ctx, []DownloadStatus{task}, "pause only aria2 task after Telegram file errors")
	if len(paused) == 0 {
		return nil
	}
	r.logger.Warn("Paused only active aria2 task after frequent Telegram file errors",
		zap.String("gid", task.GID),
		zap.Duration("pause_duration", r.cfg.PauseDuration))
	return r.resumeAfterPause(ctx, paused)
}

func (r *TelegramErrorRegulator) activeOwnedTasks(ctx context.Context) ([]DownloadStatus, error) {
	registeredGIDs, err := r.store.GIDs(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load tdl aria2 task registry")
	}
	downloadPrefix, err := aria2DownloadURLPrefix(r.publicBaseURL)
	if err != nil {
		return nil, err
	}

	var active []DownloadStatus
	if err := r.retryAction(ctx, "query aria2 active tasks", func(actionCtx context.Context) error {
		var err error
		active, err = r.client.TellActive(actionCtx)
		return err
	}); err != nil {
		return nil, errors.Wrap(err, "query aria2 active tasks")
	}

	result := make([]DownloadStatus, 0, len(active))
	for _, task := range active {
		status := normalizedAria2Status(task.Status)
		if status != aria2StatusActive {
			continue
		}
		if !isTDLAria2Task(task, registeredGIDs, downloadPrefix) {
			continue
		}
		result = append(result, task)
	}
	return result, nil
}

func (r *TelegramErrorRegulator) forcePauseTasks(ctx context.Context, tasks []DownloadStatus, action string) []DownloadStatus {
	paused := make([]DownloadStatus, 0, len(tasks))
	for _, task := range tasks {
		if task.GID == "" {
			continue
		}
		gid := task.GID
		err := r.retryAction(ctx, action, func(actionCtx context.Context) error {
			return r.client.ForcePause(actionCtx, gid)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return paused
			}
			r.logger.Warn("Failed to pause aria2 task while regulating Telegram file errors",
				zap.String("gid", gid),
				zap.Error(err))
			continue
		}
		paused = append(paused, task)
	}
	return paused
}

func (r *TelegramErrorRegulator) resumeAfterPause(ctx context.Context, tasks []DownloadStatus) error {
	if len(tasks) == 0 {
		return nil
	}

	timer := time.NewTimer(r.cfg.PauseDuration)
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

	for _, task := range tasks {
		if task.GID == "" {
			continue
		}
		gid := task.GID
		err := r.retryAction(ctx, "unpause aria2 task after Telegram file error backoff", func(actionCtx context.Context) error {
			return r.client.Unpause(actionCtx, gid)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			r.logger.Warn("Failed to resume aria2 task after Telegram file error backoff",
				zap.String("gid", gid),
				zap.Error(err))
			continue
		}
		r.logger.Info("Resumed aria2 task after Telegram file error backoff",
			zap.String("gid", gid))
	}
	return nil
}

func (r *TelegramErrorRegulator) retryAction(ctx context.Context, action string, fn func(context.Context) error) error {
	actionCtx, cancel := context.WithTimeout(ctx, r.cfg.ActionTimeout)
	defer cancel()

	return retryAria2ConnectionWithInterval(actionCtx, r.logger, action, time.Second, func() error {
		return fn(actionCtx)
	})
}

func sortRegulationTasks(tasks []DownloadStatus) {
	sort.SliceStable(tasks, func(i, j int) bool {
		left := aria2TaskInfo(tasks[i])
		right := aria2TaskInfo(tasks[j])
		if left.CompletedLength != right.CompletedLength {
			return left.CompletedLength > right.CompletedLength
		}
		if left.RemainingLength != right.RemainingLength {
			return left.RemainingLength < right.RemainingLength
		}
		return tasks[i].GID < tasks[j].GID
	})
}

func (r *TelegramErrorRegulator) lock() {
	r.mu.Lock()
}

func (r *TelegramErrorRegulator) unlock() {
	r.mu.Unlock()
}

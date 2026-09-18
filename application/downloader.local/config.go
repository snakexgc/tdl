package local

import (
	"context"
	"time"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte/config"
)

const (
	pollIntervalField    = "poll_interval_ms"
	shutdownTimeoutField = "shutdown_timeout_seconds"
)

func Manifest() manifest.Manifest {
	minPoll, maxPoll, minShutdown, maxShutdown := int64(100), int64(3600000), int64(1), int64(300)
	return manifest.Manifest{
		ID: ID, Title: "本地下载器",
		Provides: []manifest.Port{manifest.PortOf[ports.DownloadExecutor](ports.DownloadExecutorName, 1, 0)},
		Config: []manifest.ConfigField{
			{Name: pollIntervalField, Title: "待执行任务扫描间隔（毫秒）", Type: manifest.Int, Default: 5000, Min: &minPoll, Max: &maxPoll},
			{Name: shutdownTimeoutField, Title: "停机暂停记录超时（秒）", Type: manifest.Int, Default: 5, Min: &minShutdown, Max: &maxShutdown},
		},
	}
}

func (s *service) PrepareConfig(ctx context.Context, view config.View) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var poll, shutdown int64
	if err := view.Get(pollIntervalField, &poll); err != nil {
		return nil, err
	}
	if err := view.Get(shutdownTimeoutField, &shutdown); err != nil {
		return nil, err
	}
	return func() {
		s.worker.pollInterval.Store(int64(time.Duration(poll) * time.Millisecond))
		s.worker.shutdownTimeout.Store(int64(time.Duration(shutdown) * time.Second))
		select {
		case s.worker.configChanged <- struct{}{}:
		default:
		}
	}, nil
}

func (s *service) Reconfigure(ctx context.Context, view config.View) error {
	commit, err := s.PrepareConfig(ctx, view)
	if err != nil {
		return err
	}
	commit()
	return nil
}

func (d *Worker) scanInterval() time.Duration {
	if value := d.pollInterval.Load(); value > 0 {
		return time.Duration(value)
	}
	return internalDownloadPollInterval
}

func (d *Worker) pauseTimeout() time.Duration {
	if value := d.shutdownTimeout.Load(); value > 0 {
		return time.Duration(value)
	}
	return internalDownloadShutdownPauseTimeout
}

// ValidateConfiguration validates offline edits without acquiring resources.
func ValidateConfiguration(ctx context.Context, view config.View) error {
	_, err := (&service{}).PrepareConfig(ctx, view)
	return err
}

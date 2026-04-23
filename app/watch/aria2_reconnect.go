package watch

import (
	"context"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/go-faster/errors"
	"go.uber.org/zap"
)

const aria2TellWaitingBatchSize = 1000

const aria2StatusActive = "active"

type aria2ReconnectClient interface {
	TellActive(ctx context.Context) ([]aria2DownloadStatus, error)
	TellWaiting(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error)
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
}

func suspendTDLAria2TasksForReconnect(ctx context.Context, client aria2ReconnectClient, store *aria2TaskStore, publicBaseURL string, logger *zap.Logger) ([]string, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	registeredGIDs, err := store.GIDs(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "load tdl aria2 task registry")
	}
	downloadPrefix, err := aria2DownloadURLPrefix(publicBaseURL)
	if err != nil {
		return nil, err
	}

	candidates, err := listTDLAria2ReconnectTasks(ctx, client, registeredGIDs, downloadPrefix, logger)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		logger.Info("No tdl aria2 tasks need pausing before reconnect")
		return nil, nil
	}

	paused := make([]string, 0, len(candidates))
	for _, task := range candidates {
		gid := task.GID
		err := retryAria2Connection(ctx, logger, "force pause aria2 task", func() error {
			return client.ForcePause(ctx, gid)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return paused, err
			}
			logger.Warn("Skipping aria2 task that could not be paused",
				zap.String("gid", gid),
				zap.String("status", task.Status),
				zap.Error(err))
			continue
		}

		logger.Info("Paused tdl aria2 task before Telegram reconnect",
			zap.String("gid", gid),
			zap.String("status", task.Status))
		paused = append(paused, gid)
	}

	return paused, nil
}

func resumeTDLAria2Tasks(ctx context.Context, client aria2ReconnectClient, gids []string, logger *zap.Logger) error {
	if logger == nil {
		logger = zap.NewNop()
	}

	for _, gid := range uniqueGIDs(gids) {
		err := retryAria2Connection(ctx, logger, "unpause aria2 task", func() error {
			return client.Unpause(ctx, gid)
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			logger.Warn("Skipping aria2 task that could not be resumed",
				zap.String("gid", gid),
				zap.Error(err))
			continue
		}

		logger.Info("Resumed tdl aria2 task after Telegram reconnect",
			zap.String("gid", gid))
	}
	return nil
}

func listTDLAria2ReconnectTasks(ctx context.Context, client aria2ReconnectClient, registeredGIDs map[string]struct{}, downloadPrefix string, logger *zap.Logger) ([]aria2DownloadStatus, error) {
	var active []aria2DownloadStatus
	if err := retryAria2Connection(ctx, logger, "query aria2 active tasks", func() error {
		var err error
		active, err = client.TellActive(ctx)
		return err
	}); err != nil {
		return nil, errors.Wrap(err, "query aria2 active tasks")
	}

	result := make([]aria2DownloadStatus, 0, len(active))
	seen := map[string]struct{}{}
	appendOwned := func(task aria2DownloadStatus) {
		if task.GID == "" {
			return
		}
		if _, ok := seen[task.GID]; ok {
			return
		}
		if !isTDLAria2Task(task, registeredGIDs, downloadPrefix) {
			return
		}
		seen[task.GID] = struct{}{}
		result = append(result, task)
	}

	for _, task := range active {
		if task.Status == "" || task.Status == aria2StatusActive {
			appendOwned(task)
		}
	}

	for offset := 0; ; {
		var waiting []aria2DownloadStatus
		if err := retryAria2Connection(ctx, logger, "query aria2 waiting tasks", func() error {
			var err error
			waiting, err = client.TellWaiting(ctx, offset, aria2TellWaitingBatchSize)
			return err
		}); err != nil {
			return nil, errors.Wrap(err, "query aria2 waiting tasks")
		}

		for _, task := range waiting {
			if task.Status == "waiting" {
				appendOwned(task)
			}
		}
		if len(waiting) < aria2TellWaitingBatchSize {
			break
		}
		offset += len(waiting)
	}

	return result, nil
}

func isTDLAria2Task(task aria2DownloadStatus, registeredGIDs map[string]struct{}, downloadPrefix string) bool {
	if _, ok := registeredGIDs[task.GID]; ok {
		return true
	}

	for _, file := range task.Files {
		for _, uri := range file.URIs {
			if strings.HasPrefix(uri.URI, downloadPrefix) {
				return true
			}
		}
	}
	return false
}

func retryAria2Connection(ctx context.Context, logger *zap.Logger, action string, fn func() error) error {
	return retryAria2ConnectionWithInterval(ctx, logger, action, aria2ConnectRetryInterval, fn)
}

func retryAria2ConnectionWithInterval(ctx context.Context, logger *zap.Logger, action string, retryInterval time.Duration, fn func() error) error {
	if logger == nil {
		logger = zap.NewNop()
	}
	if retryInterval <= 0 {
		retryInterval = aria2ConnectRetryInterval
	}

	for {
		err := fn()
		if err == nil {
			return nil
		}
		if errors.Is(err, context.Canceled) || !isAria2ConnectionError(err) {
			return err
		}

		logger.Warn("Cannot connect to aria2 RPC, retrying",
			zap.String("action", action),
			zap.Duration("retry_interval", retryInterval),
			zap.Error(err))
		color.Yellow("⚠️ Cannot connect to aria2 RPC while trying to %s: %v", action, err)
		color.Yellow("🔄 Retrying in %v...", retryInterval)

		timer := time.NewTimer(retryInterval)
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
	}
}

func aria2DownloadURLPrefix(baseURL string) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "parse public_base_url")
	}
	if u.Scheme == "" || u.Host == "" {
		return "", errors.New("public_base_url must include scheme and host")
	}

	u.Path = path.Join(strings.TrimSuffix(u.Path, "/"), "download") + "/"
	return u.String(), nil
}

func mergeUniqueGIDs(base, add []string) []string {
	seen := map[string]struct{}{}
	merged := make([]string, 0, len(base)+len(add))
	for _, gid := range base {
		if gid == "" {
			continue
		}
		if _, ok := seen[gid]; ok {
			continue
		}
		seen[gid] = struct{}{}
		merged = append(merged, gid)
	}
	for _, gid := range add {
		if gid == "" {
			continue
		}
		if _, ok := seen[gid]; ok {
			continue
		}
		seen[gid] = struct{}{}
		merged = append(merged, gid)
	}
	return merged
}

func uniqueGIDs(gids []string) []string {
	return mergeUniqueGIDs(nil, gids)
}

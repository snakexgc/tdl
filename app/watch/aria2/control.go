package aria2

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

const aria2ControlBatchSize = 1000

type ControlClient interface {
	GetGlobalOptions(ctx context.Context) (map[string]string, error)
	ChangeGlobalOption(ctx context.Context, options map[string]any) error
	TellStatus(ctx context.Context, gid string) (DownloadStatus, error)
	TellActive(ctx context.Context) ([]DownloadStatus, error)
	TellWaiting(ctx context.Context, offset, num int) ([]DownloadStatus, error)
	TellStopped(ctx context.Context, offset, num int) ([]DownloadStatus, error)
	Pause(ctx context.Context, gid string) error
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
	AddURI(ctx context.Context, uri string, opts AddURIOptions) (string, error)
	AddTorrent(ctx context.Context, data []byte, opts AddURIOptions) (string, error)
	Remove(ctx context.Context, gid string) error
	RemoveDownloadResult(ctx context.Context, gid string) error
}

type Controller struct {
	client        ControlClient
	store         *TaskStore
	publicBaseURL string
	logger        *zap.Logger
}

type Aria2Controller = Controller

type TaskInfo struct {
	GID             string
	Status          string
	TotalLength     int64
	CompletedLength int64
	RemainingLength int64
	ErrorCode       string
	ErrorMessage    string
}

type Aria2TaskInfo = TaskInfo

type Overview struct {
	TotalTasks       int
	RemainingTasks   int
	RemainingBytes   int64
	StatusCounts     map[string]int
	RetryCandidates  []TaskInfo
	RetryBytes       int64
	RetryStatusCount map[string]int
}

type Aria2Overview = Overview

type ActionResult struct {
	Matched int
	Changed int
	Skipped int
	Errors  []string
}

type Aria2ActionResult = ActionResult

func NewController(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Controller {
	if cfg == nil {
		cfg = config.Get()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Controller{
		client:        NewClient(cfg.Aria2),
		store:         NewTaskStore(kvd, downloadLinkTTL(cfg.HTTP)),
		publicBaseURL: cfg.HTTP.PublicBaseURL,
		logger:        logger,
	}
}

func NewAria2Controller(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Controller {
	return NewController(cfg, kvd, logger)
}

func (c *Controller) Overview(ctx context.Context) (Overview, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return Overview{}, err
	}

	overview := Overview{
		TotalTasks:       len(tasks),
		StatusCounts:     map[string]int{},
		RetryStatusCount: map[string]int{},
	}
	for _, task := range tasks {
		info := aria2TaskInfo(task)
		status := normalizedAria2Status(task.Status)
		overview.StatusCounts[status]++
		if info.RemainingLength > 0 && status != "complete" {
			overview.RemainingTasks++
			overview.RemainingBytes += info.RemainingLength
		}
		if isRetryableStoppedAria2Task(info) {
			overview.RetryCandidates = append(overview.RetryCandidates, info)
			overview.RetryBytes += info.RemainingLength
			overview.RetryStatusCount[status]++
		}
	}
	sortAria2TaskInfos(overview.RetryCandidates)
	return overview, nil
}

func (c *Controller) GlobalOptions(ctx context.Context) (map[string]string, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("aria2 controller is not initialized")
	}
	return c.client.GetGlobalOptions(ctx)
}

func (c *Controller) SetGlobalDir(ctx context.Context, dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return errors.New("aria2 download dir is empty")
	}
	if c == nil || c.client == nil {
		return errors.New("aria2 controller is not initialized")
	}
	return c.client.ChangeGlobalOption(ctx, map[string]any{"dir": dir})
}

func (c *Controller) AddURL(ctx context.Context, uri string, opts AddURIOptions) (string, error) {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return "", errors.New("download url is empty")
	}
	if c == nil || c.client == nil {
		return "", errors.New("aria2 controller is not initialized")
	}
	return c.client.AddURI(ctx, uri, opts)
}

func (c *Controller) AddTorrent(ctx context.Context, data []byte, opts AddURIOptions) (string, error) {
	if c == nil || c.client == nil {
		return "", errors.New("aria2 controller is not initialized")
	}
	return c.client.AddTorrent(ctx, data, opts)
}

func (c *Controller) TellStatus(ctx context.Context, gid string) (DownloadStatus, error) {
	if c == nil || c.client == nil {
		return DownloadStatus{}, errors.New("aria2 controller is not initialized")
	}
	return c.client.TellStatus(ctx, gid)
}

func (c *Controller) ActiveTasks(ctx context.Context) ([]DownloadStatus, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("aria2 controller is not initialized")
	}
	tasks, err := c.client.TellActive(ctx)
	if err != nil {
		return nil, err
	}
	sortDownloadStatuses(tasks)
	return tasks, nil
}

func (c *Controller) WaitingTasks(ctx context.Context) ([]DownloadStatus, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("aria2 controller is not initialized")
	}
	tasks, err := c.listWaiting(ctx)
	if err != nil {
		return nil, err
	}
	sortDownloadStatuses(tasks)
	return tasks, nil
}

func (c *Controller) StoppedTasks(ctx context.Context) ([]DownloadStatus, error) {
	if c == nil || c.client == nil {
		return nil, errors.New("aria2 controller is not initialized")
	}
	tasks, err := c.listStopped(ctx)
	if err != nil {
		return nil, err
	}
	sortDownloadStatuses(tasks)
	return tasks, nil
}

func (c *Controller) PauseTask(ctx context.Context, gid string) error {
	if c == nil || c.client == nil {
		return errors.New("aria2 controller is not initialized")
	}
	return c.client.Pause(ctx, gid)
}

func (c *Controller) UnpauseTask(ctx context.Context, gid string) error {
	if c == nil || c.client == nil {
		return errors.New("aria2 controller is not initialized")
	}
	return c.client.Unpause(ctx, gid)
}

func (c *Controller) RemoveTask(ctx context.Context, gid string) error {
	if c == nil || c.client == nil {
		return errors.New("aria2 controller is not initialized")
	}
	return c.client.Remove(ctx, gid)
}

func (c *Controller) ClearStopped(ctx context.Context) (ActionResult, error) {
	tasks, err := c.StoppedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		if task.GID == "" {
			result.Skipped++
			continue
		}
		result.Matched++
		if err := c.client.RemoveDownloadResult(ctx, task.GID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
			continue
		}
		result.Changed++
	}
	return result, nil
}

func (c *Controller) PauseAll(ctx context.Context) (ActionResult, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		status := normalizedAria2Status(task.Status)
		switch status {
		case "active", "waiting":
			result.Matched++
			if err := c.client.ForcePause(ctx, task.GID); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
				continue
			}
			result.Changed++
		default:
			result.Skipped++
		}
	}
	return result, nil
}

func (c *Controller) StartAll(ctx context.Context) (ActionResult, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		status := normalizedAria2Status(task.Status)
		if status != "paused" {
			result.Skipped++
			continue
		}
		result.Matched++
		if err := c.client.Unpause(ctx, task.GID); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
			continue
		}
		result.Changed++
	}
	return result, nil
}

func (c *Controller) RetryStopped(ctx context.Context) (ActionResult, error) {
	tasks, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	downloadPrefix, _ := c.downloadPrefix()
	var result ActionResult
	for _, task := range tasks {
		info := aria2TaskInfo(task)
		if !isRetryableStoppedAria2Task(info) {
			result.Skipped++
			continue
		}
		result.Matched++

		record := records[task.GID]
		downloadURL := record.DownloadURL
		if downloadURL == "" {
			downloadURL = firstTDLTaskURI(task, downloadPrefix)
		}
		if downloadURL == "" {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: cannot find original download url", task.GID))
			continue
		}

		next := record
		next.GID = ""
		next.DownloadURL = downloadURL
		if next.Dir == "" && next.Out == "" {
			next.Dir, next.Out = maybeAria2PathOptions(task)
		}
		gid, err := c.client.AddURI(ctx, downloadURL, AddURIOptions{
			Dir: next.Dir,
			Out: next.Out,
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
			continue
		}

		next.GID = gid
		next.CreatedAt = time.Now()
		if err := c.store.Add(ctx, next); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: persist new gid %s: %v", task.GID, gid, err))
			continue
		}
		if err := c.client.RemoveDownloadResult(ctx, task.GID); err != nil {
			c.logger.Warn("Failed to remove old aria2 result after retry",
				zap.String("old_gid", task.GID),
				zap.String("new_gid", gid),
				zap.Error(err))
		}
		if err := c.store.Remove(ctx, task.GID); err != nil {
			c.logger.Warn("Failed to remove old aria2 task record after retry",
				zap.String("old_gid", task.GID),
				zap.String("new_gid", gid),
				zap.Error(err))
		}

		result.Changed++
	}
	return result, nil
}

func (c *Controller) listOwnedTasks(ctx context.Context) ([]DownloadStatus, map[string]TaskRecord, error) {
	if c == nil || c.client == nil {
		return nil, nil, errors.New("aria2 controller is not initialized")
	}

	records, err := c.store.Records(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err, "load tdl aria2 task registry")
	}
	registeredGIDs := make(map[string]struct{}, len(records))
	for gid := range records {
		registeredGIDs[gid] = struct{}{}
	}
	downloadPrefix, _ := c.downloadPrefix()

	var all []DownloadStatus
	active, err := c.client.TellActive(ctx)
	if err != nil {
		return nil, nil, errors.Wrap(err, "query aria2 active tasks")
	}
	all = append(all, active...)

	waiting, err := c.listWaiting(ctx)
	if err != nil {
		return nil, nil, err
	}
	all = append(all, waiting...)

	stopped, err := c.listStopped(ctx)
	if err != nil {
		return nil, nil, err
	}
	all = append(all, stopped...)

	seen := map[string]struct{}{}
	owned := make([]DownloadStatus, 0, len(all))
	for _, task := range all {
		if task.GID == "" {
			continue
		}
		if _, ok := seen[task.GID]; ok {
			continue
		}
		if !isTDLAria2Task(task, registeredGIDs, downloadPrefix) {
			continue
		}
		seen[task.GID] = struct{}{}
		owned = append(owned, task)
	}
	sort.SliceStable(owned, func(i, j int) bool {
		return owned[i].GID < owned[j].GID
	})
	return owned, records, nil
}

func (c *Controller) listWaiting(ctx context.Context) ([]DownloadStatus, error) {
	var all []DownloadStatus
	for offset := 0; ; {
		batch, err := c.client.TellWaiting(ctx, offset, aria2ControlBatchSize)
		if err != nil {
			return nil, errors.Wrap(err, "query aria2 waiting tasks")
		}
		all = append(all, batch...)
		if len(batch) < aria2ControlBatchSize {
			return all, nil
		}
		offset += len(batch)
	}
}

func (c *Controller) listStopped(ctx context.Context) ([]DownloadStatus, error) {
	var all []DownloadStatus
	for offset := 0; ; {
		batch, err := c.client.TellStopped(ctx, offset, aria2ControlBatchSize)
		if err != nil {
			return nil, errors.Wrap(err, "query aria2 stopped tasks")
		}
		all = append(all, batch...)
		if len(batch) < aria2ControlBatchSize {
			return all, nil
		}
		offset += len(batch)
	}
}

func (c *Controller) downloadPrefix() (string, error) {
	if c == nil || c.publicBaseURL == "" {
		return "", nil
	}
	return aria2DownloadURLPrefix(c.publicBaseURL)
}

func aria2TaskInfo(task DownloadStatus) TaskInfo {
	total := parseAria2Length(task.TotalLength)
	completed := parseAria2Length(task.CompletedLength)
	if total == 0 && len(task.Files) > 0 {
		for _, file := range task.Files {
			total += parseAria2Length(file.Length)
			completed += parseAria2Length(file.CompletedLength)
		}
	}
	remaining := total - completed
	if remaining < 0 {
		remaining = 0
	}

	return TaskInfo{
		GID:             task.GID,
		Status:          normalizedAria2Status(task.Status),
		TotalLength:     total,
		CompletedLength: completed,
		RemainingLength: remaining,
		ErrorCode:       task.ErrorCode,
		ErrorMessage:    task.ErrorMessage,
	}
}

func TaskInfoFromStatus(task DownloadStatus) TaskInfo {
	return aria2TaskInfo(task)
}

func parseAria2Length(value string) int64 {
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func TaskName(task DownloadStatus) string {
	if task.Bittorrent != nil && task.Bittorrent.Info != nil && strings.TrimSpace(task.Bittorrent.Info.Name) != "" {
		return strings.TrimSpace(task.Bittorrent.Info.Name)
	}

	for _, file := range task.Files {
		if strings.TrimSpace(file.Path) != "" {
			name := filepath.Base(filepath.Clean(file.Path))
			if name != "." && name != string(filepath.Separator) {
				return name
			}
		}
		for _, uri := range file.URIs {
			if strings.TrimSpace(uri.URI) == "" {
				continue
			}
			parsed, err := url.Parse(uri.URI)
			if err == nil && parsed.Path != "" {
				name := filepath.Base(parsed.Path)
				if name != "." && name != string(filepath.Separator) {
					return name
				}
			}
			return uri.URI
		}
	}
	if task.GID != "" {
		return task.GID
	}
	return "(unknown)"
}

func normalizedAria2Status(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func isRetryableStoppedAria2Task(info TaskInfo) bool {
	switch info.Status {
	case "error", "removed":
		return true
	case "complete":
		return info.RemainingLength > 0
	default:
		return false
	}
}

func firstTDLTaskURI(task DownloadStatus, downloadPrefix string) string {
	var first string
	for _, file := range task.Files {
		for _, uri := range file.URIs {
			if uri.URI == "" {
				continue
			}
			if first == "" {
				first = uri.URI
			}
			if downloadPrefix != "" && strings.HasPrefix(uri.URI, downloadPrefix) {
				return uri.URI
			}
		}
	}
	return first
}

func maybeAria2PathOptions(task DownloadStatus) (dir, out string) {
	if len(task.Files) == 0 || task.Files[0].Path == "" {
		return "", ""
	}
	path := filepath.Clean(task.Files[0].Path)
	return filepath.Dir(path), filepath.Base(path)
}

func sortAria2TaskInfos(values []TaskInfo) {
	sort.SliceStable(values, func(i, j int) bool {
		return values[i].GID < values[j].GID
	})
}

func sortDownloadStatuses(values []DownloadStatus) {
	sort.SliceStable(values, func(i, j int) bool {
		return values[i].GID < values[j].GID
	})
}

func downloadLinkTTL(cfg config.HTTPConfig) time.Duration {
	if cfg.DownloadLinkTTLHours <= 0 {
		return 0
	}
	return time.Duration(cfg.DownloadLinkTTLHours) * time.Hour
}

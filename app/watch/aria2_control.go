package watch

import (
	"context"
	"fmt"
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

type aria2ControlClient interface {
	TellActive(ctx context.Context) ([]aria2DownloadStatus, error)
	TellWaiting(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error)
	TellStopped(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error)
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
	AddURI(ctx context.Context, uri string, opts aria2AddURIOptions) (string, error)
	RemoveDownloadResult(ctx context.Context, gid string) error
}

type Aria2Controller struct {
	client        aria2ControlClient
	store         *aria2TaskStore
	publicBaseURL string
	logger        *zap.Logger
}

type Aria2TaskInfo struct {
	GID             string
	Status          string
	TotalLength     int64
	CompletedLength int64
	RemainingLength int64
	ErrorCode       string
	ErrorMessage    string
}

type Aria2Overview struct {
	TotalTasks       int
	RemainingTasks   int
	RemainingBytes   int64
	StatusCounts     map[string]int
	RetryCandidates  []Aria2TaskInfo
	RetryBytes       int64
	RetryStatusCount map[string]int
}

type Aria2ActionResult struct {
	Matched int
	Changed int
	Skipped int
	Errors  []string
}

func NewAria2Controller(cfg *config.Config, kvd storage.Storage, logger *zap.Logger) *Aria2Controller {
	if cfg == nil {
		cfg = config.Get()
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	return &Aria2Controller{
		client:        newAria2Client(cfg.Aria2),
		store:         newAria2TaskStore(kvd, downloadLinkTTL(cfg.HTTP)),
		publicBaseURL: cfg.HTTP.PublicBaseURL,
		logger:        logger,
	}
}

func (c *Aria2Controller) Overview(ctx context.Context) (Aria2Overview, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return Aria2Overview{}, err
	}

	overview := Aria2Overview{
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

func (c *Aria2Controller) PauseAll(ctx context.Context) (Aria2ActionResult, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return Aria2ActionResult{}, err
	}

	var result Aria2ActionResult
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

func (c *Aria2Controller) StartAll(ctx context.Context) (Aria2ActionResult, error) {
	tasks, _, err := c.listOwnedTasks(ctx)
	if err != nil {
		return Aria2ActionResult{}, err
	}

	var result Aria2ActionResult
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

func (c *Aria2Controller) RetryStopped(ctx context.Context) (Aria2ActionResult, error) {
	tasks, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return Aria2ActionResult{}, err
	}

	downloadPrefix, _ := c.downloadPrefix()
	var result Aria2ActionResult
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
		gid, err := c.client.AddURI(ctx, downloadURL, aria2AddURIOptions{
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

func (c *Aria2Controller) listOwnedTasks(ctx context.Context) ([]aria2DownloadStatus, map[string]aria2TaskRecord, error) {
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

	var all []aria2DownloadStatus
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
	owned := make([]aria2DownloadStatus, 0, len(all))
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

func (c *Aria2Controller) listWaiting(ctx context.Context) ([]aria2DownloadStatus, error) {
	var all []aria2DownloadStatus
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

func (c *Aria2Controller) listStopped(ctx context.Context) ([]aria2DownloadStatus, error) {
	var all []aria2DownloadStatus
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

func (c *Aria2Controller) downloadPrefix() (string, error) {
	if c == nil || c.publicBaseURL == "" {
		return "", nil
	}
	return aria2DownloadURLPrefix(c.publicBaseURL)
}

func aria2TaskInfo(task aria2DownloadStatus) Aria2TaskInfo {
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

	return Aria2TaskInfo{
		GID:             task.GID,
		Status:          normalizedAria2Status(task.Status),
		TotalLength:     total,
		CompletedLength: completed,
		RemainingLength: remaining,
		ErrorCode:       task.ErrorCode,
		ErrorMessage:    task.ErrorMessage,
	}
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

func normalizedAria2Status(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "active"
	}
	return status
}

func isRetryableStoppedAria2Task(info Aria2TaskInfo) bool {
	switch info.Status {
	case "error", "removed":
		return true
	case "complete":
		return info.RemainingLength > 0
	default:
		return false
	}
}

func firstTDLTaskURI(task aria2DownloadStatus, downloadPrefix string) string {
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

func maybeAria2PathOptions(task aria2DownloadStatus) (dir, out string) {
	if len(task.Files) == 0 || task.Files[0].Path == "" {
		return "", ""
	}
	path := filepath.Clean(task.Files[0].Path)
	return filepath.Dir(path), filepath.Base(path)
}

func sortAria2TaskInfos(values []Aria2TaskInfo) {
	sort.SliceStable(values, func(i, j int) bool {
		return values[i].GID < values[j].GID
	})
}

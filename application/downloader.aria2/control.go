package aria2

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-faster/errors"
	"go.uber.org/zap"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	aria2ControlBatchSize = 1000
	aria2DownloaderName   = "aria2"
	aria2StatusComplete   = "complete"
	aria2StatusPaused     = "paused"
	aria2StatusError      = "error"
	aria2StatusWaiting    = "waiting"
)

type Controller struct {
	account         types.AccountID
	client          ControlClient
	store           ports.Aria2Repository
	publicBaseURL   string
	links           *atomic.Pointer[linkPolicy]
	connections     int
	connectionLimit atomic.Int64
	logger          *zap.Logger
}

type Options struct {
	Observations  ports.Aria2Observations
	LinkTTL       time.Duration
	Account       types.AccountID
	Client        ports.Aria2Client
	Store         ports.Aria2Repository
	PublicBaseURL string
	Connections   int
	Limit         int
}

func NewController(opts Options, logger *zap.Logger) *Controller {
	if logger == nil {
		logger = zap.NewNop()
	}
	logger = logger.With(zap.String("component", "downloader.aria2"))
	return &Controller{account: opts.Account, client: opts.Client, store: opts.Store, publicBaseURL: opts.PublicBaseURL, connections: opts.Connections, logger: logger}
}

func (c *Controller) Name() string {
	return aria2DownloaderName
}

func (c *Controller) transferConnections() int {
	if value := c.connectionLimit.Load(); value > 0 {
		return int(value)
	}
	return c.connections
}

// Submit implements download.Submitter. Task creation and link generation stay
// in watch/HTTP; this controller owns only aria2 RPC submission and bookkeeping.
func (c *Controller) Submit(ctx context.Context, submission types.DownloadSubmission) (types.DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	if c == nil || c.client == nil || c.store == nil {
		return types.DownloadResult{}, fmt.Errorf("aria2 controller is not initialized: %w", ports.ErrDownloadNotAccepted)
	}
	account := c.account
	if account == "" {
		account = types.DefaultAccount
	}
	if submission.Account != "" && submission.Account != account {
		return types.DownloadResult{}, errors.New("download account mismatch")
	}
	if strings.TrimSpace(submission.DownloadURL) == "" {
		return types.DownloadResult{}, errors.New("download url is empty")
	}

	gid, err := c.client.AddURI(ctx, submission.DownloadURL, AddURIOptions{
		Dir:         submission.Dir,
		Out:         submission.Out,
		Connections: c.transferConnections(),
	})
	if err != nil {
		return types.DownloadResult{}, errors.Wrap(err, "add aria2 uri")
	}
	if gid == "" {
		return types.DownloadResult{}, errors.New("aria2 returned empty gid")
	}
	if err := c.store.Add(ctx, TaskRecord{
		GID:         gid,
		TaskID:      submission.TaskID,
		DownloadURL: submission.DownloadURL,
		Dir:         submission.Dir,
		Out:         submission.Out,
		CreatedAt:   time.Now(),
	}); err != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		return types.DownloadResult{Account: account, Target: c.Name(), ID: gid}, errors.Join(fmt.Errorf("register aria2 task %s: %w", gid, err), c.client.Remove(cleanup, gid))
	}

	c.logger.Info("Submitted aria2 task",
		zap.String("gid", gid),
		zap.String("task_id", submission.TaskID),
		zap.String("target_path", submission.FullPath))
	return types.DownloadResult{Account: account, Target: c.Name(), ID: gid}, nil
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
		if info.RemainingLength > 0 && status != aria2StatusComplete {
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

func (c *Controller) TellStatus(ctx context.Context, gid string) (DownloadStatus, error) {
	if c == nil || c.client == nil || c.store == nil {
		return DownloadStatus{}, errors.New("aria2 controller is not initialized")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return DownloadStatus{}, err
	}
	if _, owned := records[gid]; !owned {
		return DownloadStatus{}, errors.New("aria2 task is not registered to this account")
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
	return c.registeredTasks(ctx, tasks)
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
	return c.registeredTasks(ctx, tasks)
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
	return c.registeredTasks(ctx, tasks)
}

func (c *Controller) registeredTasks(ctx context.Context, tasks []DownloadStatus) ([]DownloadStatus, error) {
	if c.store == nil {
		return nil, errors.New("aria2 task storage is unavailable")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return nil, err
	}
	owned := make([]DownloadStatus, 0, len(tasks))
	for _, task := range tasks {
		if _, ok := records[task.GID]; ok {
			owned = append(owned, task)
		}
	}
	return owned, nil
}

func (c *Controller) PauseTask(ctx context.Context, gid string) error {
	return c.controlOne(ctx, gid, "pause")
}

func (c *Controller) UnpauseTask(ctx context.Context, gid string) error {
	return c.controlOne(ctx, gid, controlResume)
}

func (c *Controller) RemoveTask(ctx context.Context, gid string) error {
	return c.controlOne(ctx, gid, "delete")
}

func (c *Controller) ClearStopped(ctx context.Context) (ActionResult, error) {
	tasks, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		if task.Status != aria2StatusComplete && task.Status != aria2StatusError && task.Status != string(types.DownloadRemoved) {
			result.Skipped++
			continue
		}
		result.Matched++
		if _, err := c.performControl(ctx, records[task.GID], "delete", false); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
			continue
		}
		result.Changed++
	}
	return result, nil
}

func (c *Controller) PauseAll(ctx context.Context) (ActionResult, error) {
	tasks, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		status := normalizedAria2Status(task.Status)
		switch status {
		case aria2StatusActive, aria2StatusWaiting:
			result.Matched++
			changed, err := c.performControl(ctx, records[task.GID], "pause", true)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
				continue
			}
			if changed {
				result.Changed++
			} else {
				result.Skipped++
			}
		default:
			result.Skipped++
		}
	}
	return result, nil
}

func (c *Controller) StartAll(ctx context.Context) (ActionResult, error) {
	tasks, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return ActionResult{}, err
	}

	var result ActionResult
	for _, task := range tasks {
		status := normalizedAria2Status(task.Status)
		if status != aria2StatusPaused {
			result.Skipped++
			continue
		}
		result.Matched++
		changed, err := c.performControl(ctx, records[task.GID], controlResume, false)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
			continue
		}
		if changed {
			result.Changed++
		} else {
			result.Skipped++
		}
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

		changed, err := c.retryTask(ctx, record, task, downloadURL)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", task.GID, err))
		}
		if changed {
			result.Changed++
		} else if err == nil {
			result.Skipped++
		}
	}
	return result, nil
}

func (c *Controller) listOwnedTasks(ctx context.Context) ([]DownloadStatus, map[string]TaskRecord, error) {
	if c == nil || c.client == nil || c.store == nil {
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
		if _, registered := registeredGIDs[task.GID]; !registered {
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
	all, err := listTaskPages(ctx, c.client.TellWaiting)
	if err != nil {
		return nil, errors.Wrap(err, "query aria2 waiting tasks")
	}
	return all, nil
}

func (c *Controller) listStopped(ctx context.Context) ([]DownloadStatus, error) {
	all, err := listTaskPages(ctx, c.client.TellStopped)
	if err != nil {
		return nil, errors.Wrap(err, "query aria2 stopped tasks")
	}
	return all, nil
}

func listTaskPages(ctx context.Context, query func(context.Context, int, int) ([]DownloadStatus, error)) ([]DownloadStatus, error) {
	var all []DownloadStatus
	for offset := 0; ; {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := query(ctx, offset, aria2ControlBatchSize)
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if len(batch) < aria2ControlBatchSize {
			return all, nil
		}
		offset += len(batch)
	}
}

func (c *Controller) downloadPrefix() (string, error) {
	if c == nil {
		return "", nil
	}
	base := currentBaseURL(c.links, c.publicBaseURL)
	if base == "" {
		return "", nil
	}
	return aria2DownloadURLPrefix(base)
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
		return aria2StatusActive
	}
	return status
}

func isRetryableStoppedAria2Task(info TaskInfo) bool {
	switch info.Status {
	case aria2StatusError, string(types.DownloadRemoved):
		return true
	case aria2StatusComplete:
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

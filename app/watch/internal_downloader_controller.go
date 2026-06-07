package watch

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-faster/errors"

	httpdl "github.com/iyear/tdl/app/http"
	"github.com/iyear/tdl/core/storage"
	"github.com/iyear/tdl/pkg/config"
)

// PauseAll pauses every download that is not already stopped (complete/paused/removed).
func (c *InternalDownloadController) PauseAll(ctx context.Context) (InternalDownloadActionResult, error) {
	records, err := c.store.Records(ctx)
	if err != nil {
		return InternalDownloadActionResult{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		switch r.Status {
		case InternalDownloadStatusQueued, InternalDownloadStatusActive, InternalDownloadStatusError:
			ids = append(ids, r.ID)
		}
	}
	return c.Pause(ctx, ids)
}

// StartAll re-queues every download that is paused or in an error state.
func (c *InternalDownloadController) StartAll(ctx context.Context) (InternalDownloadActionResult, error) {
	records, err := c.store.Records(ctx)
	if err != nil {
		return InternalDownloadActionResult{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		switch r.Status {
		case InternalDownloadStatusPaused, InternalDownloadStatusError:
			ids = append(ids, r.ID)
		}
	}
	return c.Start(ctx, ids)
}

// DeleteAllByStatus deletes every download whose status is in the given list.
// If statuses is empty it defaults to [complete, error] — a safe "purge finished"
// operation analogous to aria2ng's "Purge Completed/Error Downloads".
func (c *InternalDownloadController) DeleteAllByStatus(ctx context.Context, statuses []string) (InternalDownloadActionResult, error) {
	if len(statuses) == 0 {
		statuses = []string{InternalDownloadStatusComplete, InternalDownloadStatusError}
	}
	statusSet := make(map[string]struct{}, len(statuses))
	for _, s := range statuses {
		statusSet[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return InternalDownloadActionResult{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		if _, ok := statusSet[r.Status]; ok {
			ids = append(ids, r.ID)
		}
	}
	return c.Delete(ctx, ids)
}

// Overview returns per-status counts for all tracked downloads.
func (c *InternalDownloadController) Overview(ctx context.Context) (InternalDownloadOverview, error) {
	if c == nil || c.store == nil {
		return InternalDownloadOverview{}, nil
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return InternalDownloadOverview{}, err
	}
	var ov InternalDownloadOverview
	for _, r := range records {
		ov.Total++
		switch r.Status {
		case InternalDownloadStatusActive:
			ov.Active++
		case InternalDownloadStatusQueued:
			ov.Queued++
		case InternalDownloadStatusPaused:
			ov.Paused++
		case InternalDownloadStatusComplete:
			ov.Complete++
		case InternalDownloadStatusError:
			ov.Error++
		}
	}
	return ov, nil
}

type InternalDownloadController struct {
	store *internalTaskStore
	kv    storage.Storage
}

func NewInternalDownloadController(kvd storage.Storage) *InternalDownloadController {
	return &InternalDownloadController{
		store: newInternalTaskStore(kvd),
		kv:    kvd,
	}
}

func (c *InternalDownloadController) List(ctx context.Context) ([]InternalDownloadInfo, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("internal download controller is not initialized")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]InternalDownloadInfo, 0, len(records))
	for _, record := range records {
		record = c.refreshRecordFromDisk(ctx, record)
		items = append(items, internalDownloadInfo(record))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (c *InternalDownloadController) AddLink(ctx context.Context, cfg *config.Config, taskID string) (InternalDownloadInfo, error) {
	if c == nil || c.kv == nil {
		return InternalDownloadInfo{}, errors.New("namespace kv storage is not configured")
	}
	if cfg == nil {
		cfg = config.Get()
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || strings.Contains(taskID, "/") {
		return InternalDownloadInfo{}, errors.New("invalid download task id")
	}

	task, ok, err := httpdl.NewTaskStore(c.kv, 0).Get(ctx, taskID)
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	if !ok {
		return InternalDownloadInfo{}, errors.New("download link record not found")
	}

	root, _, err := prepareInternalOutputRoot(cfg)
	if err != nil {
		return InternalDownloadInfo{}, err
	}
	data := internalDownloadDirData(task)
	baseDir := joinTargetPath(root, renderDownloadDir(cfg.DownloadDir, data)...)
	dir, out, fullPath := resolveTargetPath(baseDir, task.FileName)
	record := internalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       dir,
		Out:       out,
		Path:      fullPath,
		Total:     task.FileSize,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}
	if existing, ok, err := c.store.Get(ctx, record.ID); err != nil {
		return InternalDownloadInfo{}, err
	} else if ok && !existing.CreatedAt.IsZero() {
		record.CreatedAt = existing.CreatedAt
	}
	if err := c.store.Save(ctx, record); err != nil {
		return InternalDownloadInfo{}, err
	}
	return internalDownloadInfo(record), nil
}

func (c *InternalDownloadController) Pause(ctx context.Context, ids []string) (InternalDownloadActionResult, error) {
	return c.updateStatuses(ctx, ids, func(record internalDownloadRecord) (internalDownloadRecord, bool) {
		switch record.Status {
		case InternalDownloadStatusComplete, InternalDownloadStatusRemoved, InternalDownloadStatusPaused:
			return record, false
		default:
			record.Status = InternalDownloadStatusPaused
			record.Error = ""
			return record, true
		}
	})
}

func (c *InternalDownloadController) Start(ctx context.Context, ids []string) (InternalDownloadActionResult, error) {
	return c.updateStatuses(ctx, ids, func(record internalDownloadRecord) (internalDownloadRecord, bool) {
		if record.Status == InternalDownloadStatusComplete || record.Status == InternalDownloadStatusRemoved {
			return record, false
		}
		record.Status = InternalDownloadStatusQueued
		record.Error = ""
		return record, true
	})
}

func (c *InternalDownloadController) Delete(ctx context.Context, ids []string) (InternalDownloadActionResult, error) {
	var result InternalDownloadActionResult
	if c == nil || c.store == nil {
		return result, errors.New("internal download controller is not initialized")
	}
	for _, id := range uniqueInternalDownloadIDs(ids) {
		record, ok, err := c.store.Get(ctx, id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !ok {
			result.Skipped++
			continue
		}
		result.Matched++
		if record.Status != InternalDownloadStatusComplete {
			record.Status = InternalDownloadStatusRemoved
			_ = c.store.Save(ctx, record)
		}
		if record.Status != InternalDownloadStatusComplete && record.Path != "" {
			if err := os.Remove(record.Path); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: delete partial file: %v", id, err))
			}
		}
		if err := c.store.Remove(ctx, id); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		result.Changed++
	}
	return result, nil
}

func (c *InternalDownloadController) updateStatuses(ctx context.Context, ids []string, update func(internalDownloadRecord) (internalDownloadRecord, bool)) (InternalDownloadActionResult, error) {
	var result InternalDownloadActionResult
	if c == nil || c.store == nil {
		return result, errors.New("internal download controller is not initialized")
	}
	for _, id := range uniqueInternalDownloadIDs(ids) {
		record, ok, err := c.store.Get(ctx, id)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !ok {
			result.Skipped++
			continue
		}
		next, changed := update(record)
		if !changed {
			result.Skipped++
			continue
		}
		result.Matched++
		if err := c.store.Save(ctx, next); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		result.Changed++
	}
	return result, nil
}

func (c *InternalDownloadController) refreshRecordFromDisk(ctx context.Context, record internalDownloadRecord) internalDownloadRecord {
	if record.Path == "" {
		return record
	}
	stat, err := os.Stat(record.Path)
	if err != nil || stat.IsDir() {
		return record
	}
	size := stat.Size()
	if record.Total > 0 && size > record.Total {
		size = record.Total
	}
	changed := record.Completed != size
	record.Completed = size
	if record.Total > 0 && size >= record.Total && record.Status != InternalDownloadStatusComplete {
		record.Status = InternalDownloadStatusComplete
		record.Error = ""
		changed = true
		markPersistentDownloadTaskDownloaded(ctx, c.kv, record.TaskID)
	}
	if changed {
		_ = c.store.Save(ctx, record)
	}
	return record
}

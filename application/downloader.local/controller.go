package local

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/go-faster/errors"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

// PauseAll pauses every download that is not already stopped (complete/paused/removed).
func (c *Controller) PauseAll(ctx context.Context) (types.LocalDownloadActionResult, error) {
	records, err := c.store.Records(ctx)
	if err != nil {
		return types.LocalDownloadActionResult{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		switch r.Status {
		case types.LocalDownloadStatusQueued, types.LocalDownloadStatusActive, types.LocalDownloadStatusError:
			ids = append(ids, r.ID)
		}
	}
	return c.Pause(ctx, ids)
}

// StartAll re-queues every download that is paused or in an error state.
func (c *Controller) StartAll(ctx context.Context) (types.LocalDownloadActionResult, error) {
	records, err := c.store.Records(ctx)
	if err != nil {
		return types.LocalDownloadActionResult{}, err
	}
	ids := make([]string, 0, len(records))
	for _, r := range records {
		switch r.Status {
		case types.LocalDownloadStatusPaused, types.LocalDownloadStatusError:
			ids = append(ids, r.ID)
		}
	}
	return c.Start(ctx, ids)
}

// DeleteAllByStatus deletes every download whose status is in the given list.
// If statuses is empty it defaults to [complete, error] — a safe "purge finished"
// operation analogous to aria2ng's "Purge Completed/Error Downloads".
func (c *Controller) DeleteAllByStatus(ctx context.Context, statuses []string) (types.LocalDownloadActionResult, error) {
	if len(statuses) == 0 {
		statuses = []string{types.LocalDownloadStatusComplete, types.LocalDownloadStatusError}
	}
	statusSet := make(map[string]struct{}, len(statuses))
	for _, s := range statuses {
		statusSet[strings.ToLower(strings.TrimSpace(s))] = struct{}{}
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return types.LocalDownloadActionResult{}, err
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
func (c *Controller) Overview(ctx context.Context) (types.LocalDownloadOverview, error) {
	if c == nil || c.store == nil {
		return types.LocalDownloadOverview{}, nil
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return types.LocalDownloadOverview{}, err
	}
	var ov types.LocalDownloadOverview
	for _, r := range records {
		ov.Total++
		switch r.Status {
		case types.LocalDownloadStatusActive:
			ov.Active++
		case types.LocalDownloadStatusQueued:
			ov.Queued++
		case types.LocalDownloadStatusPaused:
			ov.Paused++
		case types.LocalDownloadStatusComplete:
			ov.Complete++
		case types.LocalDownloadStatusError:
			ov.Error++
		}
	}
	return ov, nil
}

type Controller struct {
	store ports.LocalDownloadRepository
}

func NewController(store ports.LocalDownloadRepository) *Controller { return &Controller{store: store} }

func (c *Controller) List(ctx context.Context) ([]types.LocalDownloadInfo, error) {
	if c == nil || c.store == nil {
		return nil, errors.New("local download controller is not initialized")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]types.LocalDownloadInfo, 0, len(records))
	for _, record := range records {
		record = c.refreshRecordFromDisk(ctx, record)
		items = append(items, localDownloadInfo(record))
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].CreatedAt.After(items[j].CreatedAt)
	})
	return items, nil
}

func (c *Controller) Pause(ctx context.Context, ids []string) (types.LocalDownloadActionResult, error) {
	return c.updateStatuses(ctx, ids, func(record types.LocalDownloadRecord) (types.LocalDownloadRecord, bool) {
		switch record.Status {
		case types.LocalDownloadStatusComplete, types.LocalDownloadStatusRemoved, types.LocalDownloadStatusPaused:
			return record, false
		default:
			record.Status = types.LocalDownloadStatusPaused
			record.Error = ""
			return record, true
		}
	})
}

func (c *Controller) Start(ctx context.Context, ids []string) (types.LocalDownloadActionResult, error) {
	return c.updateStatuses(ctx, ids, func(record types.LocalDownloadRecord) (types.LocalDownloadRecord, bool) {
		if record.Status == types.LocalDownloadStatusComplete || record.Status == types.LocalDownloadStatusRemoved {
			return record, false
		}
		record.Status = types.LocalDownloadStatusQueued
		record.StartedAt = nil
		record.Error = ""
		return record, true
	})
}

func (c *Controller) Delete(ctx context.Context, ids []string) (types.LocalDownloadActionResult, error) {
	var result types.LocalDownloadActionResult
	if c == nil || c.store == nil {
		return result, errors.New("local download controller is not initialized")
	}
	for _, id := range uniqueLocalDownloadIDs(ids) {
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
		changed, err := c.store.Update(ctx, id, func(current *types.LocalDownloadRecord) bool {
			if current.Status != types.LocalDownloadStatusComplete {
				current.Status = types.LocalDownloadStatusRemoved
			}
			record = *current
			return true
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if !changed {
			result.Skipped++
			continue
		}

		if record.Status != types.LocalDownloadStatusComplete && record.Path != "" {
			if err := os.Remove(record.Path); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: delete partial file: %v", id, err))
				continue
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

func (c *Controller) updateStatuses(ctx context.Context, ids []string, update func(types.LocalDownloadRecord) (types.LocalDownloadRecord, bool)) (types.LocalDownloadActionResult, error) {
	var result types.LocalDownloadActionResult
	if c == nil || c.store == nil {
		return result, errors.New("local download controller is not initialized")
	}
	for _, id := range uniqueLocalDownloadIDs(ids) {
		changed, err := c.store.Update(ctx, id, func(record *types.LocalDownloadRecord) bool {
			next, changed := update(*record)
			if changed {
				*record = next
			}
			return changed
		})
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
			continue
		}
		if changed {
			result.Matched++
			result.Changed++
		} else {
			result.Skipped++
		}
	}

	return result, nil
}

func (c *Controller) refreshRecordFromDisk(ctx context.Context, record types.LocalDownloadRecord) types.LocalDownloadRecord {
	if record.Path == "" {
		return record
	}
	stat, err := os.Stat(record.Path)
	if err != nil || stat.IsDir() {
		return record
	}
	changed, err := c.store.Update(ctx, record.ID, func(current *types.LocalDownloadRecord) bool {
		record = *current
		if current.Status == types.LocalDownloadStatusRemoved {
			return false
		}
		size := stat.Size()
		if current.Total > 0 && size > current.Total {
			size = current.Total
		}
		changed := current.Completed != size
		current.Completed = size
		if current.Total > 0 && size >= current.Total && current.Status != types.LocalDownloadStatusPaused && current.Status != types.LocalDownloadStatusComplete {
			current.Status = types.LocalDownloadStatusComplete
			current.Error = ""
			changed = true
		}
		record = *current
		return changed
	})
	if err == nil && changed && record.Status == types.LocalDownloadStatusComplete {
		_ = c.store.MarkDownloaded(ctx, record.TaskID)
	}
	return record
}

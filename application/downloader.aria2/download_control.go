package aria2

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/snakexgc/tdl/interfaces/types"
)

// ListTasks uses the account registry as authority. A matching URL alone does
// not grant control of another account's task on a shared aria2 daemon.
func (c *Controller) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	items, records, err := c.listOwnedTasks(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]types.DownloadTask, 0, len(items))
	for _, item := range items {
		record, owned := records[item.GID]
		if !owned {
			continue
		}
		info := aria2TaskInfo(item)
		speed, _ := strconv.ParseInt(item.DownloadSpeed, 10, 64)
		tasks = append(tasks, types.DownloadTask{ID: item.GID, TaskID: record.TaskID, FileName: record.Out, Dir: record.Dir, Out: record.Out, Path: filepath.Join(record.Dir, record.Out), Status: info.Status, Total: info.TotalLength, Completed: info.CompletedLength, Error: strings.TrimSpace(item.ErrorCode + " " + item.ErrorMessage), DownloadSpeed: speed, CreatedAt: record.CreatedAt})
	}
	return tasks, nil
}

func (c *Controller) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	var result types.DownloadActionResult
	if action != "pause" && action != controlResume && action != "delete" {
		return result, fmt.Errorf("unsupported aria2 action %q", action)
	}
	if c == nil || c.store == nil || c.client == nil {
		return result, fmt.Errorf("aria2 controller is unavailable")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return result, err
	}
	for _, id := range ids {
		record, ok := records[id]
		if !ok {
			result.Skipped++
			continue
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		result.Matched++
		changed, err := c.performControl(ctx, record, action, false)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", id, err))
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

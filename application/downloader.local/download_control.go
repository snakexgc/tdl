package local

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/types"
)

func (c *Controller) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	items, err := c.List(ctx)
	if err != nil {
		return nil, err
	}
	tasks := make([]types.DownloadTask, 0, len(items))
	for _, item := range items {
		tasks = append(tasks, types.DownloadTask{ID: item.ID, TaskID: item.TaskID, FileName: item.FileName, Dir: item.Dir, Out: item.Out, Path: item.Path, Status: item.Status, Total: item.Total, Completed: item.Completed, Error: item.Error, DownloadSpeed: item.DownloadSpeed, EtaSeconds: item.EtaSeconds, ElapsedSeconds: item.ElapsedSeconds, StartedAt: item.StartedAt, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt})
	}
	return tasks, nil
}

func (c *Controller) ChangeTasks(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	var result types.InternalDownloadActionResult
	var err error
	switch action {
	case "pause":
		result, err = c.Pause(ctx, ids)
	case "resume":
		result, err = c.Start(ctx, ids)
	case "delete":
		result, err = c.Delete(ctx, ids)
	default:
		return types.DownloadActionResult{}, fmt.Errorf("unsupported local action %q", action)
	}
	return types.DownloadActionResult(result), err
}

package aria2

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/snakexgc/tdl/interfaces/types"
)

const controlResume = "resume"

// performControl fences observations while the bounded RPC is in flight. The
// repository owns the fence across independently created console/API handles.
func (c *Controller) performControl(ctx context.Context, record TaskRecord, action string, force bool) (changed bool, err error) {
	return c.performOwnedControl(ctx, record, action, force, "")
}

func (c *Controller) performOwnedControl(ctx context.Context, record TaskRecord, action string, force bool, owner string) (changed bool, err error) {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lease, reserved, err := c.store.ReserveControl(call, record, time.Now().Add(time.Minute))
	if err != nil {
		return false, err
	}
	if !reserved {
		return false, fmt.Errorf("aria2 task changed or another control is in progress")
	}
	lease.PauseOwner = owner
	if owner != "" && record.Status == aria2StatusPaused && record.PauseOwner == "" {
		lease.PauseOwner = "" // an explicit user pause retains precedence
	}
	nextStatus, remove := "", false
	defer func() {
		err = errors.Join(err, c.finishControl(ctx, lease, nextStatus, remove))
	}()
	status, err := c.client.TellStatus(call, record.GID)
	if err != nil {
		return false, err
	}
	switch action {
	case "pause":
		if status.Status == aria2StatusPaused || status.Status == aria2StatusComplete || status.Status == aria2StatusError {
			if owner != "" {
				lease.PauseOwner = record.PauseOwner
			}
			nextStatus = status.Status
			return owner == "" && record.PauseOwner != "" && status.Status == aria2StatusPaused, nil
		}
		if force {
			err = c.client.ForcePause(call, record.GID)
		} else {
			err = c.client.Pause(call, record.GID)
		}
		if err == nil {
			nextStatus = aria2StatusPaused
		}
	case controlResume:
		if status.Status != aria2StatusPaused {
			nextStatus = normalizedAria2Status(status.Status)
			return false, nil
		}
		err = c.client.Unpause(call, record.GID)
		if err == nil {
			nextStatus = aria2StatusWaiting
		}
	case "delete":
		if status.Status == aria2StatusComplete || status.Status == aria2StatusError || status.Status == string(types.DownloadRemoved) {
			err = c.client.RemoveDownloadResult(call, record.GID)
		} else {
			err = c.client.Remove(call, record.GID)
		}
		remove = err == nil
	default:
		return false, fmt.Errorf("unsupported aria2 action %q", action)
	}
	return err == nil, err
}

func (c *Controller) finishControl(ctx context.Context, lease TaskRecord, status string, remove bool) error {
	finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	applied, err := c.store.FinishControl(finish, lease, status, remove)
	if !applied && err == nil {
		return fmt.Errorf("aria2 control result superseded by another update")
	}
	return err
}

func (c *Controller) retryTask(ctx context.Context, record TaskRecord, task DownloadStatus, downloadURL string) (changed bool, err error) {
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	lease, reserved, err := c.store.ReserveControl(call, record, time.Now().Add(time.Minute))
	if err != nil {
		return false, err
	}
	if !reserved {
		return false, fmt.Errorf("aria2 task changed or another control is in progress")
	}
	defer func() { err = errors.Join(err, c.finishControl(ctx, lease, "", changed)) }()
	current, err := c.client.TellStatus(call, record.GID)
	if err != nil {
		return false, err
	}
	if !isRetryableStoppedAria2Task(aria2TaskInfo(current)) {
		return false, nil
	}
	dir, out := record.Dir, record.Out
	if dir == "" && out == "" {
		dir, out = maybeAria2PathOptions(task)
	}
	gid, err := c.client.AddURI(call, downloadURL, AddURIOptions{Dir: dir, Out: out, Connections: c.transferConnections()})
	if err != nil {
		return false, err
	}
	if gid == "" {
		return false, errors.New("aria2 returned empty gid")
	}
	next := TaskRecord{GID: gid, TaskID: record.TaskID, DownloadURL: downloadURL, Dir: dir, Out: out, CreatedAt: time.Now(), Status: aria2StatusWaiting}
	if err := c.store.Add(call, next); err != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		return false, errors.Join(fmt.Errorf("persist new gid %s: %w", gid, err), c.client.Remove(cleanup, gid))
	}
	// Once the replacement is registered the old execution must not be retried
	// again, even if removal of its remote result fails.
	return true, c.client.RemoveDownloadResult(call, record.GID)
}

func (c *Controller) controlOne(ctx context.Context, gid, action string) error {
	if c == nil || c.client == nil || c.store == nil {
		return fmt.Errorf("aria2 controller is unavailable")
	}
	records, err := c.store.Records(ctx)
	if err != nil {
		return err
	}
	record, owned := records[gid]
	if !owned {
		return fmt.Errorf("aria2 task is not registered to this account")
	}
	_, err = c.performControl(ctx, record, action, false)
	return err
}

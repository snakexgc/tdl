package aria2

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/ports"
)

var errAutomaticControlSkipped = errors.New("automatic control skipped: task is unregistered or pause belongs to another owner")

// automaticClient uses the same account repository and RPC fence as user
// controls. Each governor may resume only its own pauses. Manual pause clears
// ownership, so a delayed backoff/recovery cannot undo the user's choice.
type automaticClient struct {
	ports.Aria2Client
	controller *Controller
	owner      string
	resumeAny  bool
}

func (c automaticClient) ForcePause(ctx context.Context, gid string) error {
	return c.change(ctx, gid, "pause")
}

func (c automaticClient) Unpause(ctx context.Context, gid string) error {
	return c.change(ctx, gid, controlResume)
}

func (c automaticClient) change(ctx context.Context, gid, action string) error {
	records, err := c.controller.store.Records(ctx)
	if err != nil {
		return err
	}
	record, owned := records[gid]
	if !owned {
		return errAutomaticControlSkipped
	}
	if action == controlResume && (record.PauseOwner == "" || (!c.resumeAny && record.PauseOwner != c.owner)) {
		return errAutomaticControlSkipped
	}
	changed, err := c.controller.performOwnedControl(ctx, record, action, true, c.owner)
	if err != nil {
		return err
	}
	if !changed {
		return errAutomaticControlSkipped
	}
	return nil
}

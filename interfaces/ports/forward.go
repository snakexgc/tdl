package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const ForwardTasksName = "forward.tasks"

// ForwardTasks is the account-scoped persistent forwarding command and query port.
type ForwardTasks interface {
	EnqueueLinks(context.Context, []string, string, string, string, bool) ([]string, error)
	EnqueueMessage(context.Context, int64, int, string, string, string, string, bool) (string, error)
	List(context.Context) ([]types.ForwardJob, error)
	RunningCount(context.Context) (int, error)
	Pause(context.Context, []string) (types.ForwardActionResult, error)
	Resume(context.Context, []string) (types.ForwardActionResult, error)
	Delete(context.Context, []string) (types.ForwardActionResult, error)
}

// ForwardRepository persists records in the account's isolated dataset.
type ForwardRepository interface {
	Save(context.Context, types.ForwardJob) error
	// Update modifies an existing record atomically; a missing record is never created.
	Update(context.Context, string, func(*types.ForwardJob) bool) (bool, error)
	Get(context.Context, string) (types.ForwardJob, bool, error)
	Records(context.Context) ([]types.ForwardJob, error)
	Remove(context.Context, string) error
}

// ForwardTransport synchronously sends one job. Report must finish before returning.
// Implementations must return individual message errors, including album errors.
type ForwardTransport interface {
	Forward(context.Context, *types.ForwardJob, func(types.ForwardJob)) error
}

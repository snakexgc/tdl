package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadControlName = "download.control"

// DownloadControl routes explicit executor commands within one account.
type DownloadControl interface {
	Tasks(context.Context, types.AccountID, string) ([]types.DownloadTask, error)
	Control(context.Context, types.DownloadAction) (types.DownloadActionResult, error)
}

// DownloadBackend operates on an already isolated account repository. Batch
// selection and action validation belong to the control component.
type DownloadBackend interface {
	ListTasks(context.Context) ([]types.DownloadTask, error)
	ChangeTasks(context.Context, string, []string) (types.DownloadActionResult, error)
}

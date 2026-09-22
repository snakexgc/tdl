package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadControlName = "download.control"

// DownloadControl uses the current session's task repository.
type DownloadControl interface {
	Tasks(context.Context, string) ([]types.DownloadTask, error)
	Control(context.Context, types.DownloadAction) (types.DownloadActionResult, error)
}

// DownloadBackend operates on the selected session's repository. Batch
// selection and action validation belong to the control component.
type DownloadBackend interface {
	ListTasks(context.Context) ([]types.DownloadTask, error)
	ChangeTasks(context.Context, string, []string) (types.DownloadActionResult, error)
}

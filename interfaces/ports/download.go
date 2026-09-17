package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadExecutorName = "download.executor"

// DownloadExecutor accepts a task for an account. Selection belongs to the
// composition root; an error must not cause an implicit duplicate submission.
type DownloadExecutor interface {
	Name() string
	Submit(context.Context, types.DownloadSubmission) (types.DownloadResult, error)
}

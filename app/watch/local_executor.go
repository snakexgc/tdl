package watch

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

// localExecutor adapts the existing worker to the common metadata-only port.
// It resolves source data from the account's task repository, never a remote URL.
type localExecutor struct {
	account types.AccountID
	worker  *internalDownloader
}

var _ ports.DownloadExecutor = (*localExecutor)(nil)

func (*localExecutor) Name() string { return "local" }

func (e *localExecutor) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	if e == nil || e.worker == nil || e.worker.proxy == nil {
		return types.DownloadResult{}, fmt.Errorf("local executor is not initialized")
	}
	if in.Account != e.account {
		return types.DownloadResult{}, fmt.Errorf("download account mismatch")
	}
	if in.TaskID == "" || in.FullPath == "" {
		return types.DownloadResult{}, fmt.Errorf("task id and target path are required")
	}
	task, ok, err := e.worker.proxy.Tasks().Get(ctx, in.TaskID)
	if err != nil {
		return types.DownloadResult{}, err
	}
	if !ok {
		return types.DownloadResult{}, fmt.Errorf("source task is unavailable")
	}
	info, err := e.worker.Add(ctx, task, preparedFileTask{dir: in.Dir, out: in.Out, fullPath: in.FullPath})
	if err != nil {
		return types.DownloadResult{}, err
	}
	return types.DownloadResult{Account: e.account, Target: e.Name(), ID: info.ID}, nil
}

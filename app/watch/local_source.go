package watch

import (
	"context"
	"fmt"
	"io"

	httpdl "github.com/snakexgc/tdl/app/http"
	transfer "github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type localSource struct {
	proxy     *httpdl.Proxy
	scheduler *transfer.Scheduler
}

func localSourceInfo(task *httpdl.Task) types.LocalDownloadSource {
	if task == nil {
		return types.LocalDownloadSource{}
	}
	info := types.LocalDownloadSource{ID: task.ID, FileName: task.FileName, FileSize: task.FileSize, Available: task.Media != nil}
	if task.Media != nil {
		info.DC = task.Media.DC
	}
	return info
}

func (s localSource) Get(ctx context.Context, id string) (types.LocalDownloadSource, bool, error) {
	if s.proxy == nil {
		return types.LocalDownloadSource{}, false, fmt.Errorf("download source is unavailable")
	}
	task, ok, err := s.proxy.Tasks().Get(ctx, id)
	return localSourceInfo(task), ok, err
}

func (s localSource) Acquire(ctx context.Context, id string, dc int) (ports.DownloadLease, error) {
	if s.scheduler == nil {
		return nil, fmt.Errorf("download limiter is unavailable")
	}
	return s.scheduler.Acquire(ctx, id, dc)
}

func (s localSource) Stream(ctx context.Context, id string, lease ports.DownloadLease, start, end int64, w io.Writer) error {
	task, ok, err := s.proxy.Tasks().Get(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("download source disappeared")
	}
	concrete, ok := lease.(*transfer.TaskLease)
	if !ok {
		return fmt.Errorf("invalid download lease")
	}
	return s.proxy.StreamParallel(ctx, task, concrete, start, end, w)
}

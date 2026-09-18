package httpdl

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	proxy "github.com/snakexgc/tdl/application/proxy.range"
	"github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	rangeSource   struct{ proxy *downloadProxy }
	rangeTransfer struct {
		proxy *downloadProxy
		task  *downloadTask
	}
)

func (s rangeSource) CleanupExpired(ctx context.Context) error {
	if s.proxy.tasks.kv == nil || s.proxy.tasks.TTL() == 0 {
		return nil
	}
	return s.proxy.CleanupExpiredTasks(ctx)
}

func (s rangeSource) CleanupSources(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.proxy.sources.CleanupIdle(time.Now(), sourceRegistryIdleTTL)
	return nil
}

func (s rangeSource) Open(ctx context.Context, id string) (types.RangeResource, ports.RangeTransfer, bool, error) {
	task, ok, err := s.proxy.tasks.Get(ctx, id)
	if err != nil || !ok {
		return types.RangeResource{}, nil, ok, err
	}
	return rangeResource(task), rangeTransfer{proxy: s.proxy, task: task}, true, nil
}

func rangeResource(task *downloadTask) types.RangeResource {
	return types.RangeResource{ID: task.ID, FileName: task.FileName, FileSize: task.FileSize, Available: task.Media != nil}
}

func (t rangeTransfer) Ready(ctx context.Context) error {
	_, err := t.proxy.pools.Wait(ctx)
	return err
}

func (t rangeTransfer) Acquire(ctx context.Context) (ports.DownloadLease, error) {
	return t.proxy.scheduler.Acquire(ctx, t.task.ID, t.task.Media.DC)
}

func (t rangeTransfer) Stream(ctx context.Context, lease ports.DownloadLease, start, end int64, w io.Writer) error {
	concrete, ok := lease.(*comif.TaskLease)
	if !ok {
		return fmt.Errorf("invalid range lease")
	}
	return t.proxy.stream(ctx, t.task, concrete, start, end, w)
}

func (t rangeTransfer) Report(ctx context.Context, spans []types.ByteRange) error {
	ranges := make([]downloadRange, 0, len(spans))
	for _, span := range spans {
		ranges = append(ranges, downloadRange{start: span.Start, end: span.End})
	}
	_, err := t.proxy.tasks.recordHTTPDelivery(ctx, t.task.ID, t.task.FileSize, ranges, time.Now())
	return err
}

func (p *downloadProxy) handleDownload(w http.ResponseWriter, r *http.Request) {
	p.cfgMu.RLock()
	handler := p.rangeHandler
	p.cfgMu.RUnlock()
	if handler == nil {
		handler = proxy.New(rangeSource{proxy: p}, p.logger, p.clientWaitTimeout)
	}
	handler.ServeHTTP(w, r)
}

func downloadETag(task *downloadTask) string {
	if task == nil {
		return `"0"`
	}
	return proxy.ETag(rangeResource(task))
}

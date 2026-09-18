package watch

import (
	"context"
	"fmt"
	"os"

	downloadcontrol "github.com/snakexgc/tdl/application/download.control"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/util/tutil"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	localExecutorName = "local"
	httpExecutorName  = "http"
)

// preparedExecutor delays path preparation until that executor is selected.
// Remote paths never reach local filesystem operations during fallback.
type preparedExecutor struct {
	name   string
	submit func(context.Context, types.DownloadSubmission) (types.DownloadResult, error)
}

func (w *Watcher) routedRemoteRoot() string {
	if cfg := config.Get(); cfg != nil {
		return cleanTargetRoot(cfg.Aria2.Dir)
	}
	return w.runtime.outputRoot
}

func (e preparedExecutor) Name() string { return e.name }
func (e preparedExecutor) Submit(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
	return e.submit(ctx, in)
}

func (w *Watcher) submitRouted(ctx context.Context, prepared preparedFileTask, taskID string, route ports.DownloadRoute) error {
	file := prepared.file
	downloadURL, urlErr := w.runtime.proxy.BuildURL(taskID)
	executors := make([]ports.DownloadExecutor, 0, len(route.Executors))
	for _, name := range route.Executors {
		executors = append(executors, preparedExecutor{name: name, submit: func(ctx context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
			if name != localExecutorName {
				if urlErr != nil {
					return types.DownloadResult{}, fmt.Errorf("download URL unavailable: %v: %w", urlErr, ports.ErrDownloadNotAccepted)
				}
				if name == httpExecutorName {
					return (downloadcontrol.LinkExecutor{}).Submit(ctx, in)
				}
				if w.opts.DownloadSubmitter == nil {
					return types.DownloadResult{}, ports.ErrDownloadNotAccepted
				}
				data := w.downloadDirData(ctx, file)
				remote, err := w.renderTarget(ctx, w.routedRemoteRoot(), data.ID, tutil.GetInputPeerID(file.peer), data.Name, data.Time, file.msg, file.triggerMsg, file.media)
				if err != nil {
					return types.DownloadResult{}, fmt.Errorf("remote target: %v: %w", err, ports.ErrDownloadNotAccepted)
				}
				in.Dir, in.Out, in.FullPath = remote.Dir, remote.Out, remote.FullPath
				return w.opts.DownloadSubmitter.Submit(ctx, in)
			}
			if w.runtime.local == nil {
				return types.DownloadResult{}, ports.ErrDownloadNotAccepted
			}
			target := ports.NamingResult{Dir: prepared.dir, Out: prepared.out, FullPath: prepared.fullPath}
			if prepared.route == nil {
				data := w.downloadDirData(ctx, file)
				var err error
				target, err = w.renderTarget(ctx, route.LocalRoot, data.ID, tutil.GetInputPeerID(file.peer), data.Name, data.Time, file.msg, file.triggerMsg, file.media)
				if err != nil {
					return types.DownloadResult{}, fmt.Errorf("local target: %v: %w", err, ports.ErrDownloadNotAccepted)
				}
			}
			if w.opts.SkipSame {
				if info, err := os.Stat(target.FullPath); err == nil && info.Size() == file.media.Size {
					return types.DownloadResult{Account: in.Account, Target: localExecutorName, ID: in.TaskID}, nil
				}
			}
			if err := os.MkdirAll(target.Dir, 0o755); err != nil {
				return types.DownloadResult{}, fmt.Errorf("local directory: %v: %w", err, ports.ErrDownloadNotAccepted)
			}
			in.Dir, in.Out, in.FullPath = target.Dir, target.Out, target.FullPath
			return w.runtime.local.Submit(ctx, in)
		}})
	}
	result, err := downloadcontrol.NewRouter(w.reactionAccount(), executors...).Submit(ctx, types.DownloadSubmission{
		Account: w.reactionAccount(), TaskID: taskID, DownloadURL: downloadURL,
	})
	if err != nil {
		return err
	}
	if result.Target == httpExecutorName {
		w.notify(ctx, "已生成临时 HTTP 下载链接。\n文件：%s\n链接：%s", prepared.fileName, downloadURL)
	}
	return nil
}

package downloadcontrol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

// Router tries explicitly ordered executors. Only a definite rejection permits
// fallback; ambiguous transport errors must never create a second download.
type Router struct {
	account   types.AccountID
	executors []ports.DownloadExecutor
}

// LinkExecutor exposes an already registered source without creating a remote
// download. It is the safe final fallback when an automatic executor rejects.
type LinkExecutor struct{}

func (LinkExecutor) Name() string { return httpExecutor }
func (LinkExecutor) Submit(ctx context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
	if err := ctx.Err(); err != nil {
		return types.DownloadResult{}, err
	}
	if request.DownloadURL == "" || request.TaskID == "" {
		return types.DownloadResult{}, fmt.Errorf("download link is unavailable: %w", ports.ErrDownloadNotAccepted)
	}
	return types.DownloadResult{Account: request.Account, Target: httpExecutor, ID: request.TaskID}, nil
}

func NewRouter(account types.AccountID, executors ...ports.DownloadExecutor) *Router {
	if account == "" {
		account = types.DefaultAccount
	}
	return &Router{account: account, executors: append([]ports.DownloadExecutor(nil), executors...)}
}

func (r *Router) Name() string {
	for _, executor := range r.executors {
		if executor != nil {
			return executor.Name()
		}
	}
	return "unavailable"
}

func (r *Router) Submit(ctx context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
	if request.Account != r.account {
		return types.DownloadResult{}, fmt.Errorf("download account mismatch")
	}
	var rejected error
	for _, executor := range r.executors {
		if err := ctx.Err(); err != nil {
			return types.DownloadResult{}, err
		}
		if executor == nil {
			continue
		}
		result, err := executor.Submit(ctx, request)
		if err == nil {
			level := slog.LevelInfo
			if result.Skipped {
				level = slog.LevelDebug
			}
			slog.Log(ctx, level, "下载执行器已接受提交", "component", ID, "account", r.account, "task_id", request.TaskID, "executor", executor.Name(), "id", result.ID, "skipped", result.Skipped)
			return result, nil
		}
		if !errors.Is(err, ports.ErrDownloadNotAccepted) {
			return result, err
		}
		// An executor returning an ID has accepted work despite its error.
		if result.ID != "" {
			return result, err
		}
		if ctx.Err() == nil {
			slog.Warn("下载执行器拒绝提交，继续检查后续执行器", "component", ID, "account", r.account, "task_id", request.TaskID, "executor", executor.Name(), "error", err)
		}
		rejected = errors.Join(rejected, fmt.Errorf("%s: %w", executor.Name(), err))
	}
	if rejected == nil {
		rejected = ports.ErrDownloadNotAccepted
	}
	return types.DownloadResult{}, rejected
}

package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const Aria2TasksName = "aria2.tasks"

// Aria2Tasks is the account-bound control surface used by console adapters.
type Aria2Tasks interface {
	DownloadExecutor
	DownloadBackend
	GlobalOptions(context.Context) (map[string]string, error)
	TellStatus(context.Context, string) (types.Aria2DownloadStatus, error)
	ActiveTasks(context.Context) ([]types.Aria2DownloadStatus, error)
	WaitingTasks(context.Context) ([]types.Aria2DownloadStatus, error)
	StoppedTasks(context.Context) ([]types.Aria2DownloadStatus, error)
	Overview(context.Context) (types.Aria2Overview, error)
	PauseTask(context.Context, string) error
	UnpauseTask(context.Context, string) error
	RemoveTask(context.Context, string) error
	ClearStopped(context.Context) (types.Aria2ActionResult, error)
	PauseAll(context.Context) (types.Aria2ActionResult, error)
	StartAll(context.Context) (types.Aria2ActionResult, error)
	RetryStopped(context.Context) (types.Aria2ActionResult, error)
}

type Aria2ControlClient interface {
	GetGlobalOptions(ctx context.Context) (map[string]string, error)
	TellStatus(ctx context.Context, gid string) (types.Aria2DownloadStatus, error)
	TellActive(ctx context.Context) ([]types.Aria2DownloadStatus, error)
	TellWaiting(ctx context.Context, offset, num int) ([]types.Aria2DownloadStatus, error)
	TellStopped(ctx context.Context, offset, num int) ([]types.Aria2DownloadStatus, error)
	Pause(ctx context.Context, gid string) error
	ForcePause(ctx context.Context, gid string) error
	Unpause(ctx context.Context, gid string) error
	AddURI(ctx context.Context, uri string, opts types.Aria2AddURIOptions) (string, error)
	Remove(ctx context.Context, gid string) error
	RemoveDownloadResult(ctx context.Context, gid string) error
}

type Aria2Client interface {
	Aria2ControlClient
	SetMaxConcurrentDownloads(context.Context, int) error
}
type Aria2Repository interface {
	Report(context.Context, types.Aria2TaskRecord, uint64) (bool, error)
	Add(context.Context, types.Aria2TaskRecord) error
	Records(context.Context) (map[string]types.Aria2TaskRecord, error)
	GIDs(context.Context) (map[string]struct{}, error)
	Remove(context.Context, string) error
}

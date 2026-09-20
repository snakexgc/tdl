package bot

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

// localDownloadControl binds local download commands to the current session's port.
type localDownloadControl struct {
	port    ports.DownloadControl
	account types.AccountID
}

func (c *localDownloadControl) List(ctx context.Context) ([]types.DownloadTask, error) {
	return c.port.Tasks(ctx, config.DownloadExecutorLocal)
}

func (c *localDownloadControl) change(ctx context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	return c.port.Control(ctx, types.DownloadAction{Account: c.account, Executor: config.DownloadExecutorLocal, Action: action, IDs: ids})
}

func (c *localDownloadControl) Pause(ctx context.Context, ids []string) (types.DownloadActionResult, error) {
	return c.change(ctx, "pause", ids)
}

func (c *localDownloadControl) Start(ctx context.Context, ids []string) (types.DownloadActionResult, error) {
	return c.change(ctx, "resume", ids)
}

func (c *localDownloadControl) Delete(ctx context.Context, ids []string) (types.DownloadActionResult, error) {
	return c.change(ctx, "delete", ids)
}

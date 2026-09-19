package ports

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadExecutorName = "download.executor"

const DownloadRoutingName = "download.routing"

type DownloadRoute struct {
	Mode      string
	Executors []string
	LocalRoot string
}

type DownloadRouting interface {
	Route(context.Context, types.AccountID) (DownloadRoute, error)
}

// ErrDownloadNotAccepted may only be returned when no durable or remote task
// was created. A timeout or an RPC response failure alone does not prove this.
var ErrDownloadNotAccepted = errors.New("download was not accepted")

// DownloadExecutor accepts a task for an account. Selection belongs to the
// composition root; an error must not cause an implicit duplicate submission.
type DownloadExecutor interface {
	Name() string
	Submit(context.Context, types.DownloadSubmission) (types.DownloadResult, error)
}

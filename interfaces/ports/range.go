package ports

import (
	"context"
	"io"

	"github.com/snakexgc/tdl/interfaces/types"
)

// RangeMaintenance is optional for sources with expiring task/source records.
type RangeMaintenance interface {
	CleanupExpired(context.Context) error
	CleanupSources(context.Context) error
}

const RangeHandlerName = "range.handler"

// RangeTransfer binds one source snapshot to its transport, shared quota and
// delivery reporting. It never sends bytes through the event bus.
type RangeTransfer interface {
	Ready(context.Context) error
	Acquire(context.Context) (DownloadLease, error)
	Stream(context.Context, DownloadLease, int64, int64, io.Writer) error
	Report(context.Context, []types.ByteRange) error
}
type RangeSource interface {
	// A transfer implementing io.Closer is closed after every request, including
	// HEAD and validation failures that never acquire a download lease.
	Open(context.Context, string) (types.RangeResource, RangeTransfer, bool, error)
}

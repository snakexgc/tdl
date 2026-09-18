package ports

import (
	"context"
	"io"

	"github.com/snakexgc/tdl/interfaces/types"
)

// LocalDownloadRepository owns persisted local tasks. Update never inserts a
// missing task and serializes the callback with deletion and other updates.
type LocalDownloadRepository interface {
	Create(context.Context, types.LocalDownloadRecord) (types.LocalDownloadRecord, error)
	Save(context.Context, types.LocalDownloadRecord) error
	Get(context.Context, string) (types.LocalDownloadRecord, bool, error)
	Records(context.Context) (map[string]types.LocalDownloadRecord, error)
	Remove(context.Context, string) error
	Update(context.Context, string, func(*types.LocalDownloadRecord) bool) (bool, error)
	MarkDownloaded(context.Context, string) error
}

type DownloadLease interface{ Release() }

// LocalDownloadSource keeps protocol objects and shared channel leases outside
// the component. File bytes travel directly to the supplied writer.
type LocalDownloadSource interface {
	Get(context.Context, string) (types.LocalDownloadSource, bool, error)
	Acquire(context.Context, string, int) (DownloadLease, error)
	Stream(context.Context, string, DownloadLease, int64, int64, io.Writer) error
}

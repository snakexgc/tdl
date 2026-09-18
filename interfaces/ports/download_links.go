package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadLinksName = "download.links"

type DownloadLinkRepository interface {
	Remove(context.Context, string) (int, error)
}
type DownloadLinks interface {
	Remove(context.Context, types.AccountID, string) (int, error)
}

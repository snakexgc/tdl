package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadCatalogName = "download.catalog"

type DownloadCatalog interface {
	List(context.Context, types.AccountID) ([]types.DownloadLinkItem, string, error)
	Submit(context.Context, types.AccountID, []string) types.LinkSubmissionResult
}

// CatalogSource adapts storage and protocol observations. Projection and
// submission decisions belong to the consuming component.
type CatalogSource interface {
	Observe(context.Context) (types.LinkCatalogSnapshot, error)
	MarkDownloaded(context.Context, string) error
	Submission(context.Context) (LinkSubmissionResources, error)
}

type LinkSubmissionResources struct {
	// Records contains only link records, keyed by task ID rather than KV keys.
	Records       map[string][]byte
	Mode          string
	PublicBaseURL string
	RemoteDir     string
	Limit         int
	Connections   int
	Local         DownloadExecutor
	Remote        Aria2Client
	Repository    Aria2Repository
}

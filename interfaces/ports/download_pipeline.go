package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadPipelineName = "download.pipeline"

// DownloadMedia contains metadata and an opaque reference owned by one source
// collection. SDK objects and file bytes never cross this boundary.
type DownloadMedia struct {
	Token string
	Data  NamingData
}

type DownloadSources interface {
	Collect(context.Context, types.DownloadIntent) ([]DownloadMedia, DownloadRegistration, error)
}

type DownloadRegistration interface {
	Register(context.Context, string, string) (string, error)
	URL(context.Context, string) (string, error)
}

type DownloadFiles interface {
	EnsureWritable(context.Context, string) error
	EnsureDirectory(context.Context, string) error
	SameFile(context.Context, string, int64) (bool, error)
}

// DownloadResources binds connection-owned capabilities for one invocation.
// The pipeline drains them before returning; it never retains a connection.
type DownloadResources struct {
	Source    DownloadSources
	Filter    FilterRules
	Naming    NamingRules
	Routing   DownloadRouting
	Files     DownloadFiles
	Executors map[string]DownloadExecutor
	Defaults  DownloadDefaults
}

type DownloadDefaults struct {
	Mode, LocalRoot, RemoteRoot, FallbackLocalRoot string
	SkipSame                                       bool
	Limit                                          int
}

type DownloadPipeline interface {
	SubmitBatch(context.Context, types.DownloadIntent, DownloadResources) (types.DownloadSubmissionSummary, error)
}

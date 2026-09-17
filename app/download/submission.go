// Package download keeps source compatibility for legacy integrations.
package download

import (
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type (
	Submission = types.DownloadSubmission
	Result     = types.DownloadResult
	Submitter  = ports.DownloadExecutor
)

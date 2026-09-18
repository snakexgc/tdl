package watch

import "github.com/snakexgc/tdl/interfaces/types"

const (
	InternalDownloadStatusQueued   = types.InternalDownloadStatusQueued
	InternalDownloadStatusActive   = types.InternalDownloadStatusActive
	InternalDownloadStatusPaused   = types.InternalDownloadStatusPaused
	InternalDownloadStatusComplete = types.InternalDownloadStatusComplete
	InternalDownloadStatusError    = types.InternalDownloadStatusError
	InternalDownloadStatusRemoved  = types.InternalDownloadStatusRemoved
)

type (
	internalDownloadRecord       = types.LocalDownloadRecord
	InternalDownloadInfo         = types.InternalDownloadInfo
	InternalDownloadOverview     = types.InternalDownloadOverview
	InternalDownloadActionResult = types.InternalDownloadActionResult
)

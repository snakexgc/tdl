package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const DownloadIntentsName = "download.intents"

const DownloadRequestsName = "download.requests"

type DownloadRequests interface {
	Submit(context.Context, types.DownloadIntent) (types.DownloadSubmissionSummary, error)
}

type DownloadIntentResultHandler func(context.Context, types.DownloadIntent) (types.DownloadSubmissionSummary, error)

const ForwardIntentsName = "forward.intents"

type ForwardIntents interface {
	Publish(context.Context, types.ForwardIntent) error
}

type ForwardIntentHandler func(context.Context, types.ForwardIntent) error

type (
	DownloadIntents interface {
		Publish(context.Context, types.DownloadIntent) error
	}
	DownloadIntentHandler func(context.Context, types.DownloadIntent) error
)

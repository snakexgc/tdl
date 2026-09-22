package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const MessageLinksName = "trigger.message-links"

type MessageLinks interface {
	Validate(context.Context, types.AccountID, string) (string, error)
	Submit(context.Context, types.AccountID, string, MessageLinkSource, DownloadRequests) (types.DownloadSubmissionSummary, error)
}

type MessageLinkSource interface {
	Resolve(context.Context, types.AccountID, string) (types.DownloadIntent, error)
}

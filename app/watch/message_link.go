package watch

import (
	"context"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
)

// MessageLinkSubmissionResult describes a Telegram message link submitted through
// the same pipeline used by reaction-triggered watch downloads.
type MessageLinkSubmissionResult struct {
	Link      string
	PeerID    int64
	MessageID int
	Total     int
	Queued    int
	Skipped   int
}

type messageLinkSubmission struct {
	link  string
	reply chan messageLinkSubmissionResponse
}

type messageLinkSubmissionResponse struct {
	result MessageLinkSubmissionResult
	err    error
}

func ValidateTelegramMessageHTTPLink(raw string) (string, error) {
	return application.ValidateMessageLink(context.Background(), types.DefaultAccount, raw)
}

package watch

import (
	"context"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
)

// MessageLinkSubmissionResult describes a Telegram message link submitted through
// the same pipeline used by reaction-triggered watch downloads.
type MessageLinkSubmissionResult = types.DownloadSubmissionSummary

type messageLinkSubmission struct {
	ctx   context.Context
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

func validateMessageLink(ctx context.Context, opts Options, raw string) (string, error) {
	account := opts.Account
	if account == "" {
		account = types.DefaultAccount
	}
	if opts.MessageLinks != nil {
		return opts.MessageLinks.Validate(ctx, account, raw)
	}
	return application.ValidateMessageLink(ctx, account, raw)
}

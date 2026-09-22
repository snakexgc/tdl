package application

import (
	"context"

	messagelink "github.com/snakexgc/tdl/application/trigger.messagelink"
	"github.com/snakexgc/tdl/interfaces/types"
)

// ValidateMessageLink is a pure preflight. Connected requests use the injected
// MessageLinks component for account validation and submission orchestration.
func ValidateMessageLink(ctx context.Context, _ types.AccountID, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return messagelink.ValidateTelegramMessageHTTPLink(raw)
}

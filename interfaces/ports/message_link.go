package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const MessageLinksName = "trigger.message-links"

type MessageLinks interface {
	Validate(context.Context, types.AccountID, string) (string, error)
}

package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const AccountActionsName = "account.actions"

type AccountActions interface {
	CheckSpam(context.Context, types.AccountID) (bool, error)
	Switch(context.Context, types.AccountID, string) (bool, error)
}

type SpamTransport interface {
	Reply(context.Context, types.AccountID) (string, error)
}

type AccountSelection interface {
	Current(context.Context) (string, error)
	Select(context.Context, string, string) error
}

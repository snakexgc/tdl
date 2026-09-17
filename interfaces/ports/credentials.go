package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const TelegramCredentialsName = "account.telegram.credentials"

type TelegramCredentials interface {
	Resolve(context.Context, types.AccountID, string) (types.TelegramCredentials, error)
}

package ports

import (
	"context"

	"github.com/snakexgc/tdl/interfaces/types"
)

const AccountLoginName = "account.login"

// LoginChallenge contains no Telegram SDK objects or session bytes.
type LoginChallenge interface {
	Code(context.Context) (string, error)
	Password(context.Context) (string, error)
	AuthInputError(context.Context, string, error) error
}

type AccountLogin interface {
	StartPhone(context.Context, string, string) error
	Status() types.LoginStatus
	SubmitCode(string) error
	SubmitPassword(string) error
	Cancel()
	Stop(context.Context) error
}

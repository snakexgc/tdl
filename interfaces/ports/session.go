package ports

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/types"
)

const AccountSessionName = "account.session"

var ErrSessionUnauthorized = errors.New("telegram session is not authorized")

type AccountIdentity struct {
	ID                                   int64
	Username, FirstName, LastName, Phone string
	Bot, Premium, Restricted, Verified   bool
}

type AccountSession interface {
	Check(context.Context, types.AccountID) (*AccountIdentity, error)
}

type SessionTransport interface {
	Probe(context.Context) (*AccountIdentity, error)
}

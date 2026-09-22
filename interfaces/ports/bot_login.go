package ports

import (
	"context"
	"errors"
)

const BotLoginName = "account.bot_login"

var ErrLoginBusy = errors.New("login flow already active")

type LoginUser struct {
	ID                            int64
	Username, FirstName, LastName string
}

type BotLoginChallenge interface {
	LoginChallenge
	Phone(context.Context) (string, error)
}

type LoginRunner interface {
	LoginCode(context.Context, BotLoginChallenge) (*LoginUser, error)
}

type LoginMessenger interface {
	SendText(context.Context, int64, string) error
	DeleteInput(context.Context, int64, int) error
}

type BotLogin interface {
	StartCode(int64, int64, ...string) error
	Cancel(int64, int64) bool
	HandleInput(int64, int64, string, int) bool
	Busy() bool
	Stop(context.Context) error
}

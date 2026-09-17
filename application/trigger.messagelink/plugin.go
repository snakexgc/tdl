package messagelink

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const ID = "trigger.messagelink"

func Register(registry *rte.Registry) error {
	return registry.Register(manifest.Manifest{
		ID: ID, Title: "消息链接触发",
		Provides: []manifest.Port{manifest.PortOf[ports.MessageLinks](ports.MessageLinksName, 1, 0)},
	}, func() rte.Component { return &Validator{} })
}

type Validator struct{ account types.AccountID }

func (v *Validator) Init(_ context.Context, k rte.Kernel) error {
	v.account = k.Account
	return k.Provide(ports.MessageLinksName, v)
}
func (*Validator) Start(context.Context) error                    { return nil }
func (*Validator) Stop(context.Context) error                     { return nil }
func (*Validator) Reconfigure(context.Context, config.View) error { return nil }

func (v *Validator) Validate(ctx context.Context, account types.AccountID, raw string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if account != v.account {
		return "", errors.New("message link account mismatch")
	}
	return ValidateTelegramMessageHTTPLink(raw)
}

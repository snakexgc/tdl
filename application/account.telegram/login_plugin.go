package accounttelegram

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

// RegisterLogin binds the real login flow service to its owning panel lifetime.
func RegisterLogin(registry *rte.Registry, service ports.AccountLogin) error {
	if service == nil {
		return errors.New("account login service is required")
	}
	return registry.Register(manifest.Manifest{
		ID: "account.telegram.login", Title: "账号登录",
		Provides: []manifest.Port{manifest.PortOf[ports.AccountLogin](ports.AccountLoginName, 1, 0)},
	}, func() rte.Component { return &loginComponent{service: service} })
}

type loginComponent struct{ service ports.AccountLogin }

func (c *loginComponent) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.AccountLoginName, c.service)
}
func (*loginComponent) Start(context.Context) error                    { return nil }
func (c *loginComponent) Stop(ctx context.Context) error               { return c.service.Stop(ctx) }
func (*loginComponent) Reconfigure(context.Context, config.View) error { return nil }

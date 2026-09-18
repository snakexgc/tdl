package accounttelegram

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func RegisterBotLogin(registry *rte.Registry, service ports.BotLogin) error {
	if service == nil {
		return errors.New("bot login service is required")
	}
	return registry.Register(manifest.Manifest{
		ID: "account.telegram.bot_login", Title: "Bot 账号登录",
		Provides: []manifest.Port{manifest.PortOf[ports.BotLogin](ports.BotLoginName, 1, 0)},
	}, func() rte.Component { return &botLoginComponent{service} })
}

type botLoginComponent struct{ service ports.BotLogin }

func (c *botLoginComponent) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.BotLoginName, c.service)
}
func (*botLoginComponent) Start(context.Context) error                    { return nil }
func (c *botLoginComponent) Stop(ctx context.Context) error               { return c.service.Stop(ctx) }
func (*botLoginComponent) Reconfigure(context.Context, config.View) error { return nil }

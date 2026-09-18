package accounttelegram

import (
	"context"
	"errors"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func RegisterResources(registry *rte.Registry, resources ports.AccountResources) error {
	if resources == nil {
		return errors.New("account resources are required")
	}
	return registry.Register(manifest.Manifest{
		ID: "account.telegram.resources", Title: "账号连接资源",
		Provides: []manifest.Port{manifest.PortOf[ports.AccountResources](ports.AccountResourcesName, 1, 0)},
	}, func() rte.Component { return &resourceComponent{resources: resources} })
}

type resourceComponent struct{ resources ports.AccountResources }

func (c *resourceComponent) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.AccountResourcesName, c.resources)
}
func (*resourceComponent) Start(context.Context) error                    { return nil }
func (c *resourceComponent) Stop(ctx context.Context) error               { return c.resources.Stop(ctx) }
func (*resourceComponent) Reconfigure(context.Context, config.View) error { return nil }

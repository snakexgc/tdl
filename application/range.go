package application

import (
	"context"
	"fmt"

	proxy "github.com/snakexgc/tdl/application/proxy.range"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func RangeHost(ctx context.Context, account types.AccountID, handler *proxy.Handler, stores ...*config.Store) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if err := proxy.Register(registry, handler); err != nil {
		return nil, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values, err := componentValues(ctx, proxy.ID, stores...)
	if err != nil {
		return nil, err
	}
	host, err := registry.Build(account, nil, values)
	if err != nil {
		return nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	return host, nil
}

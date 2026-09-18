package application

import (
	"context"
	"fmt"

	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func Aria2DownloadHost(ctx context.Context, account types.AccountID, manager *aria2.Manager, finished chan<- error, stores ...*config.Store) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if err := aria2.Register(registry, manager, finished); err != nil {
		return nil, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values, err := componentValues(ctx, aria2.ID, stores...)
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

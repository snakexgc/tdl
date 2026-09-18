package application

import (
	"context"
	"fmt"

	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func LocalDownloadHost(ctx context.Context, account types.AccountID, worker *local.Worker, stores ...*rteconfig.Store) (*rte.Runtime, ports.DownloadExecutor, error) {
	registry := rte.NewRegistry()
	if err := local.Register(registry, worker); err != nil {
		return nil, nil, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values := map[string]map[string]any{}
	if len(stores) > 0 && stores[0] != nil {
		document, err := stores[0].Load(ctx, local.ID)
		if err != nil {
			return nil, nil, err
		}
		if !document.Enabled {
			return nil, nil, fmt.Errorf("required download component %s is disabled", local.ID)
		}
		values[local.ID] = document.Values
	}
	host, err := registry.Build(account, nil, values)
	if err != nil {
		return nil, nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.DownloadExecutorName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.DownloadExecutor), nil
}

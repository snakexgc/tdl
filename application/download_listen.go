package application

import (
	"context"
	"fmt"

	downloadlisten "github.com/snakexgc/tdl/application/download.listen"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func DownloadListenHost(ctx context.Context, account types.AccountID, repository ports.ListenRepository, filters types.ListenFilters, store *config.Store) (*rte.Runtime, ports.DownloadListener, error) {
	registry := rte.NewRegistry()
	if err := downloadlisten.Register(registry, repository, filters); err != nil {
		return nil, nil, err
	}
	values, err := componentValues(ctx, downloadlisten.ID, store)
	if err != nil {
		return nil, nil, err
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
	value, err := host.Resolve(ports.DownloadListenName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.DownloadListener), nil
}

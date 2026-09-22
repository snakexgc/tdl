package application

import (
	"context"
	"fmt"

	downloadcontrol "github.com/snakexgc/tdl/application/download.control"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func DownloadControl(account types.AccountID, backends map[string]ports.DownloadBackend) ports.DownloadControl {
	return downloadcontrol.New(account, backends)
}

func DownloadControlHost(ctx context.Context, account types.AccountID, backends map[string]ports.DownloadBackend, stores ...*config.Store) (*rte.Runtime, ports.DownloadControl, error) {
	registry := rte.NewRegistry()
	if err := downloadcontrol.Register(registry, backends); err != nil {
		return nil, nil, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values, err := componentValues(ctx, downloadcontrol.ID, stores...)
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
	value, err := host.Resolve(ports.DownloadControlName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.DownloadControl), nil
}

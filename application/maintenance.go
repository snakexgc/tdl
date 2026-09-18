package application

import (
	"context"
	"fmt"

	maintenance "github.com/snakexgc/tdl/application/storage.maintenance"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func MaintenanceHost(ctx context.Context, account types.AccountID, repository ports.CleanupRepository) (*rte.Runtime, ports.KVMaintenance, error) {
	registry := rte.NewRegistry()
	if err := maintenance.Register(registry, repository); err != nil {
		return nil, nil, err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	host, err := registry.Build(account, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.KVMaintenanceName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.KVMaintenance), nil
}

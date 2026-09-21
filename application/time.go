package application

import (
	"context"
	"fmt"

	timesync "github.com/snakexgc/tdl/application/time.sync"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TimeHost(ctx context.Context, account types.AccountID, store *config.Store, probe ports.TimeProbe) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if err := timesync.Register(registry, probe); err != nil {
		return nil, err
	}
	values, err := componentValues(ctx, timesync.ID, store)
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

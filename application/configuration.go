package application

import (
	"context"
	"fmt"

	configuration "github.com/snakexgc/tdl/application/configuration.manager"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func ConfigurationHost(ctx context.Context, account types.AccountID, service *configuration.Service) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if err := configuration.Register(registry, service); err != nil {
		return nil, err
	}
	host, err := registry.Build(account, nil, nil)
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

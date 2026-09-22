package application

import (
	"context"
	"fmt"

	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func AccountResourceHost(ctx context.Context, account types.AccountID, resources ports.AccountResources, probes ...ports.SessionTransport) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if err := accounttelegram.RegisterResources(registry, resources); err != nil {
		return nil, err
	}
	if len(probes) > 0 {
		if err := accounttelegram.RegisterSession(registry, probes[0]); err != nil {
			return nil, err
		}
	}
	if account == "" {
		account = types.DefaultAccount
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

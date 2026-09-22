package application

import (
	"context"
	"fmt"

	account "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func BotLoginHost(ctx context.Context, id types.AccountID, login ports.BotLogin) (*rte.Runtime, ports.BotLogin, error) {
	registry := rte.NewRegistry()
	if err := account.RegisterBotLogin(registry, login); err != nil {
		return nil, nil, err
	}
	if id == "" {
		id = types.DefaultAccount
	}
	host, err := registry.Build(id, nil, nil)
	if err != nil {
		return nil, nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.BotLoginName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.BotLogin), nil
}

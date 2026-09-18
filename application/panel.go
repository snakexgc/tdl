package application

import (
	"context"
	"fmt"

	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	panel "github.com/snakexgc/tdl/application/panel.webui"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func PanelHost(ctx context.Context, account types.AccountID, opts panel.Options) (*rte.Runtime, error) {
	registry := rte.NewRegistry()
	if opts.Login != nil {
		if err := accounttelegram.RegisterLogin(registry, opts.Login); err != nil {
			return nil, err
		}
	}
	if err := panel.Register(registry, opts); err != nil {
		return nil, err
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

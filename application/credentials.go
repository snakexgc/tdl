package application

import (
	"context"
	"fmt"

	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TelegramPreset(name string) (types.TelegramApp, error) { return accounttelegram.Preset(name) }
func ValidateTelegramCredentials(settings types.TelegramCredentialsConfig) error {
	return accounttelegram.Validate(settings)
}

func ResolveTelegramCredentials(ctx context.Context, account types.AccountID, legacy string, settings types.TelegramCredentialsConfig) (types.TelegramCredentials, error) {
	registry, err := Registry()
	if err != nil {
		return types.TelegramCredentials{}, err
	}
	host, err := registry.Build(account, map[string]bool{accounttelegram.ID: true}, map[string]map[string]any{
		accounttelegram.ID: {"api_id": settings.APIID, "api_hash": settings.APIHash, "builtin_preset": settings.BuiltinPreset, "use_builtin": settings.UseBuiltin},
	})
	if err != nil {
		return types.TelegramCredentials{}, err
	}
	defer func() { _ = host.Stop(ctx) }()
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			return types.TelegramCredentials{}, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.TelegramCredentialsName)
	if err != nil {
		return types.TelegramCredentials{}, err
	}
	return value.(ports.TelegramCredentials).Resolve(ctx, account, legacy)
}

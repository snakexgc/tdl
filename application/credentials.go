package application

import (
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	"github.com/snakexgc/tdl/interfaces/types"
)

func TelegramPreset(name string) (types.TelegramApp, error) { return accounttelegram.Preset(name) }
func ValidateTelegramCredentials(settings types.TelegramCredentialsConfig) error {
	return accounttelegram.Validate(settings)
}

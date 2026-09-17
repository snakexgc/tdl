package tclient

import (
	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/types"
)

const (
	AppBuiltin = "builtin"
	AppDesktop = "desktop"
)

type App = types.TelegramApp

// Retain the legacy lookup API; component-owned presets are immutable.
var Apps = func() map[string]App {
	builtin, _ := application.TelegramPreset(AppBuiltin)
	desktop, _ := application.TelegramPreset(AppDesktop)
	return map[string]App{AppBuiltin: builtin, AppDesktop: desktop}
}()

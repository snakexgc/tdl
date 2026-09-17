// Package application is the only component integration point. Explicit
// registration avoids process-wide init state and permits isolated runtimes.
package application

import (
	accounttelegram "github.com/snakexgc/tdl/application/account.telegram"
	consolebot "github.com/snakexgc/tdl/application/console.bot"
	filterrules "github.com/snakexgc/tdl/application/filter.rules"
	namingrules "github.com/snakexgc/tdl/application/naming.rules"
	notifytelegram "github.com/snakexgc/tdl/application/notify.telegram"
	messagelink "github.com/snakexgc/tdl/application/trigger.messagelink"
	reaction "github.com/snakexgc/tdl/application/trigger.reaction"
	updater "github.com/snakexgc/tdl/application/update.self"
	"github.com/snakexgc/tdl/rte"
)

func Registry() (*rte.Registry, error) {
	registry := rte.NewRegistry()
	for _, register := range []func(*rte.Registry) error{
		accounttelegram.Register,
		consolebot.Register,
		filterrules.Register,
		namingrules.Register,
		notifytelegram.Register,
		messagelink.Register,
		reaction.Register,
		updater.Register,
	} {
		if err := register(registry); err != nil {
			return nil, err
		}
	}
	return registry, nil
}

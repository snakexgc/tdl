package application

import (
	"context"
	"io/fs"

	account "github.com/snakexgc/tdl/application/account.telegram"
	configuration "github.com/snakexgc/tdl/application/configuration.manager"
	console "github.com/snakexgc/tdl/application/console.bot"
	downloadcontrol "github.com/snakexgc/tdl/application/download.control"
	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	local "github.com/snakexgc/tdl/application/downloader.local"
	filter "github.com/snakexgc/tdl/application/filter.rules"
	forwardrules "github.com/snakexgc/tdl/application/forward.rules"
	"github.com/snakexgc/tdl/application/forwarder"
	naming "github.com/snakexgc/tdl/application/naming.rules"
	notify "github.com/snakexgc/tdl/application/notify.telegram"
	panel "github.com/snakexgc/tdl/application/panel.webui"
	proxy "github.com/snakexgc/tdl/application/proxy.range"
	maintenance "github.com/snakexgc/tdl/application/storage.maintenance"
	timesync "github.com/snakexgc/tdl/application/time.sync"
	downloadtrigger "github.com/snakexgc/tdl/application/trigger.download"
	forwardtrigger "github.com/snakexgc/tdl/application/trigger.forward"
	message "github.com/snakexgc/tdl/application/trigger.messagelink"
	reaction "github.com/snakexgc/tdl/application/trigger.reaction"
	update "github.com/snakexgc/tdl/application/update.self"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

// declarations is the single feature catalog for static factories, schemas,
// pages, assets, routes and commands. Live backend bindings remain in the
// production composition root, where their resource scopes are available.
type declaration struct {
	register   func(*rte.Registry) error
	definition rte.Definition
}

func definitions(commandSets ...[]types.ConsoleCommand) ([]rte.Definition, error) {
	consoleRegister := console.Register
	if len(commandSets) > 0 {
		consoleRegister = func(registry *rte.Registry) error { return console.RegisterCommands(registry, commandSets[0]) }
	}
	ui := func(scope rte.Scope, assets fs.FS, routes []types.WebRoute) rte.Definition {
		return rte.Definition{Scope: scope, Assets: assets, Routes: routes}
	}
	declarations := []declaration{
		{nil, rte.Definition{Manifest: configuration.Manifest(), Scope: rte.ProcessScope}},
		{nil, rte.Definition{Manifest: timesync.Manifest(), Scope: rte.ProcessScope, Validate: func(ctx context.Context, view config.View) error {
			_, err := timesync.New(nil).PrepareConfig(ctx, view)
			return err
		}}},
		{account.Register, ui(rte.AccountScope, account.Assets(), account.Routes())},
		{consoleRegister, rte.Definition{Scope: rte.AccountScope, Host: "bot"}},
		{filter.Register, rte.Definition{Scope: rte.AccountScope}},
		{forwardrules.Register, rte.Definition{Scope: rte.AccountScope, Assets: forwardrules.Assets()}},
		{naming.Register, rte.Definition{Scope: rte.AccountScope}},
		{notify.Register, rte.Definition{Scope: rte.AccountScope, Host: "bot"}},
		{message.Register, rte.Definition{Scope: rte.AccountScope}},
		{reaction.Register, rte.Definition{Scope: rte.AccountScope}},
		{update.Register, ui(rte.AccountScope, update.Assets(), update.Routes())},
		{nil, rte.Definition{Manifest: panel.Manifest(), Scope: rte.ProcessScope, Assets: panel.Assets(), Routes: panel.Routes()}},
		{nil, rte.Definition{Manifest: maintenance.Manifest(), Scope: rte.AccountScope, Routes: maintenance.Routes()}},
		{nil, rte.Definition{Manifest: downloadtrigger.Manifest(), Scope: rte.ConnectionScope}},
		{nil, rte.Definition{Manifest: forwardtrigger.Manifest(), Scope: rte.ConnectionScope}},
		{nil, rte.Definition{Manifest: aria2.Manifest(), Scope: rte.AccountScope, Validate: aria2.ValidateConfiguration, Assets: aria2.Assets(), Routes: aria2.Routes()}},
		{nil, rte.Definition{Manifest: local.Manifest(), Scope: rte.ConnectionScope, Validate: local.ValidateConfiguration}},
		{nil, rte.Definition{Manifest: forwarder.Manifest(), Scope: rte.ConnectionScope, Validate: forwarder.ValidateConfiguration, Assets: forwarder.Assets(), Routes: forwarder.Routes()}},
		{nil, rte.Definition{Manifest: proxy.Manifest(), Scope: rte.ProcessScope, Validate: proxy.ValidateConfiguration}},
		{nil, rte.Definition{Manifest: downloadcontrol.Manifest(), Scope: rte.AccountScope, Validate: downloadcontrol.ValidateConfiguration, Assets: downloadcontrol.Assets(), Routes: downloadcontrol.Routes()}},
	}
	result := make([]rte.Definition, 0, len(declarations))
	for _, item := range declarations {
		definition := item.definition
		if item.register != nil {
			registry := rte.NewRegistry()
			if err := item.register(registry); err != nil {
				return nil, err
			}
			registered := registry.Definitions(definition.Scope)[0]
			registered.Host = definition.Host
			registered.Assets, registered.Routes = definition.Assets, definition.Routes
			definition = registered
		}
		result = append(result, definition)
	}
	return result, nil
}

// Catalog is available without starting services or opening configuration.
func Catalog() (*rte.Catalog, error) {
	definitions, err := definitions()
	if err != nil {
		return nil, err
	}
	return rte.NewCatalog(definitions...)
}

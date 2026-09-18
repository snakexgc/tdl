package application

import (
	"context"
	"fmt"
	"strconv"

	consolebot "github.com/snakexgc/tdl/application/console.bot"
	notifytelegram "github.com/snakexgc/tdl/application/notify.telegram"
	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const notificationTransportID = "host.notification.transport"

type notificationTransport struct{ transport ports.NotificationTransport }

func (t *notificationTransport) Init(_ context.Context, k rte.Kernel) error {
	return k.Provide(ports.NotificationTransportName, t.transport)
}
func (*notificationTransport) Start(context.Context) error                    { return nil }
func (*notificationTransport) Stop(context.Context) error                     { return nil }
func (*notificationTransport) Reconfigure(context.Context, config.View) error { return nil }

func NotificationHost(ctx context.Context, account types.AccountID, transport ports.NotificationTransport, recipients []int64) (*rte.Runtime, ports.Notifications, error) {
	host, err := botHost(ctx, account, transport, recipients, nil, false)
	if err != nil {
		return nil, nil, err
	}
	value, err := host.Resolve(ports.NotificationsName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, err
	}
	return host, value.(ports.Notifications), nil
}

// BotHost owns console policy and notifications together, with the real transport.
func BotHost(ctx context.Context, account types.AccountID, transport ports.NotificationTransport, users []int64, store *config.Store, extra ...ports.ConsoleContribution) (*rte.Runtime, ports.Console, ports.Notifications, error) {
	host, err := botHost(ctx, account, transport, users, store, true, extra...)
	if err != nil {
		return nil, nil, nil, err
	}
	console, err := host.Resolve(ports.ConsoleName)
	if err != nil {
		_ = host.Stop(context.Background())
		return nil, nil, nil, err
	}
	value, err := host.Resolve(ports.NotificationsName)
	if err != nil {
		return host, console.(ports.Console), nil, nil
	}
	return host, console.(ports.Console), value.(ports.Notifications), nil
}

func botHost(ctx context.Context, account types.AccountID, transport ports.NotificationTransport, recipients []int64, store *config.Store, withConsole bool, extra ...ports.ConsoleContribution) (*rte.Runtime, error) {
	if account == "" {
		account = types.DefaultAccount
	}
	commands, err := ConsoleCommands(ctx, store)
	if err != nil {
		return nil, err
	}
	for _, contribution := range extra {
		commands = append(commands, contribution.Commands...)
	}
	registry, err := Registry(commands)
	if err != nil {
		return nil, err
	}
	err = registry.Register(manifest.Manifest{ID: notificationTransportID, Provides: []manifest.Port{manifest.PortOf[ports.NotificationTransport](ports.NotificationTransportName, 1, 0)}}, func() rte.Component { return &notificationTransport{transport: transport} })
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(recipients))
	for _, id := range recipients {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	values := map[string]map[string]any{notifytelegram.ID: {"recipients": ids}}
	enabled := map[string]bool{notifytelegram.ID: true, notificationTransportID: true}
	if withConsole {
		enabled[consolebot.ID] = true
		values[consolebot.ID] = map[string]any{"allowed_users": ids}
	}
	if store != nil {
		for id := range values {
			document, err := store.Load(ctx, id)
			if err != nil {
				return nil, err
			}
			if !document.Enabled && id == consolebot.ID {
				return nil, fmt.Errorf("required bot component %s is disabled", id)
			}
			values[id], enabled[id] = document.Values, document.Enabled
		}
	}
	host, err := registry.Build(account, enabled, values)
	if err != nil {
		return nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running && (!withConsole || status.ID == consolebot.ID) {
			_ = host.Stop(context.Background())
			return nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	return host, nil
}

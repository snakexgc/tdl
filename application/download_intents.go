package application

import (
	"context"
	"fmt"

	trigger "github.com/snakexgc/tdl/application/trigger.download"
	forwardtrigger "github.com/snakexgc/tdl/application/trigger.forward"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func DownloadIntentHost(ctx context.Context, account types.AccountID, handler ports.DownloadIntentHandler) (*rte.Runtime, ports.DownloadIntents, error) {
	host, download, _, err := IntentHost(ctx, account, handler, nil)
	return host, download, err
}

// IntentHost owns both consumers for one connection, so shutdown drains all
// source resolution before that connection's pool is released.
func IntentHost(ctx context.Context, account types.AccountID, download ports.DownloadIntentHandler, forward ports.ForwardIntentHandler, results ...ports.DownloadIntentResultHandler) (*rte.Runtime, ports.DownloadIntents, ports.ForwardIntents, error) {
	return IntentHostStored(ctx, account, download, forward, nil, results...)
}

func IntentHostStored(ctx context.Context, account types.AccountID, download ports.DownloadIntentHandler, forward ports.ForwardIntentHandler, store *config.Store, results ...ports.DownloadIntentResultHandler) (*rte.Runtime, ports.DownloadIntents, ports.ForwardIntents, error) {
	registry := rte.NewRegistry()
	if download == nil && forward == nil {
		return nil, nil, nil, fmt.Errorf("intent host requires a consumer")
	}
	if download != nil {
		if err := trigger.Register(registry, download, 100, results...); err != nil {
			return nil, nil, nil, err
		}
	}
	if forward != nil {
		if err := forwardtrigger.Register(registry, forward, 100); err != nil {
			return nil, nil, nil, err
		}
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values := map[string]map[string]any{}
	for _, definition := range registry.Definitions(rte.ConnectionScope) {
		next, err := componentValues(ctx, definition.Manifest.ID, store)
		if err != nil {
			return nil, nil, nil, err
		}
		for id, value := range next {
			values[id] = value
		}
	}
	host, err := registry.Build(account, nil, values)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return nil, nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	var downloads ports.DownloadIntents
	var forwards ports.ForwardIntents
	if download != nil {
		value, err := host.Resolve(ports.DownloadIntentsName)
		if err != nil {
			_ = host.Stop(context.Background())
			return nil, nil, nil, err
		}
		downloads = value.(ports.DownloadIntents)
	}
	if forward != nil {
		value, err := host.Resolve(ports.ForwardIntentsName)
		if err != nil {
			_ = host.Stop(context.Background())
			return nil, nil, nil, err
		}
		forwards = value.(ports.ForwardIntents)
	}
	return host, downloads, forwards, nil
}

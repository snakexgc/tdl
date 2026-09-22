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
	enabled := map[string]bool{}
	for _, definition := range registry.Definitions(rte.ConnectionScope) {
		id := definition.Manifest.ID
		enabled[id] = true
		if store != nil {
			document, err := store.Load(ctx, id)
			if err != nil {
				return nil, nil, nil, err
			}
			values[id], enabled[id] = document.Values, document.Enabled
		}
	}
	host, err := registry.Build(account, enabled, values)
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
		downloads = downloadIntentPort{host}
	}
	if forward != nil {
		forwards = forwardIntentPort{host}
	}
	return host, downloads, forwards, nil
}

type downloadIntentPort struct{ host *rte.Runtime }

func (p downloadIntentPort) Publish(ctx context.Context, in types.DownloadIntent) error {
	value, err := p.host.Resolve(ports.DownloadIntentsName)
	if err != nil {
		return err
	}
	return value.(ports.DownloadIntents).Publish(ctx, in)
}

func (p downloadIntentPort) Submit(ctx context.Context, in types.DownloadIntent) (types.DownloadSubmissionSummary, error) {
	value, err := p.host.Resolve(ports.DownloadRequestsName)
	if err != nil {
		return types.DownloadSubmissionSummary{}, err
	}
	return value.(ports.DownloadRequests).Submit(ctx, in)
}

type forwardIntentPort struct{ host *rte.Runtime }

func (p forwardIntentPort) Listening(ctx context.Context, account types.AccountID) (types.ForwardListening, error) {
	value, err := p.host.Resolve(ports.ForwardListeningName)
	if err != nil {
		return types.ForwardListening{}, err
	}
	return value.(ports.ForwardListening).Listening(ctx, account)
}

func (p forwardIntentPort) Publish(ctx context.Context, in types.ForwardIntent) error {
	value, err := p.host.Resolve(ports.ForwardIntentsName)
	if err != nil {
		return err
	}
	return value.(ports.ForwardIntents).Publish(ctx, in)
}

package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/snakexgc/tdl/application/forwarder"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

type ForwardQueue = forwarder.Queue

func NewForwardQueue(store ports.ForwardRepository) *ForwardQueue { return forwarder.NewQueue(store) }

type ForwardOptions struct {
	Store     *config.Store
	Peers     ports.ForwardPeers
	Rules     ports.ForwardRules
	Listening ports.ForwardListening
}

// ServeForwardQueue attaches a live connection to the account queue and drains
// its RTE runnable before returning ownership of that connection to the caller.
func ServeForwardQueue(ctx context.Context, account types.AccountID, queue *ForwardQueue, transport ports.ForwardTransport, observe func(*rte.Runtime), options ...ForwardOptions) error {
	var opts ForwardOptions
	if len(options) > 0 {
		opts = options[0]
	}
	registry := rte.NewRegistry()
	completed := make(chan error, 1)
	routing := forwarder.RoutingOptions{Peers: opts.Peers, Rules: opts.Rules, Listening: opts.Listening}

	if err := forwarder.Register(registry, queue, transport, completed, forwarder.Options{Validate: ValidateMessageLink, Routing: routing}); err != nil {
		return err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values := map[string]map[string]any{}
	enabled := map[string]bool{forwarder.ID: true}

	if opts.Store != nil {
		document, err := opts.Store.Load(ctx, forwarder.ID)
		if err != nil {
			return err
		}
		values[forwarder.ID], enabled[forwarder.ID] = document.Values, document.Enabled
	}
	host, err := registry.Build(account, enabled, values)
	if err != nil {
		return err
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	if observe != nil {
		observe(host)
		defer observe(nil)
	}
	waiting := true
	for waiting {
		select {
		case <-ctx.Done():
			err, waiting = ctx.Err(), false
		case result := <-completed:
			// Disabling the SWC drains its worker, but the connection owner
			// stays available to re-enable it alongside other consumers.
			if result != nil && !errors.Is(result, context.Canceled) {
				err, waiting = result, false
			}
		}
	}
	if stopErr := host.Stop(context.Background()); stopErr != nil {
		return stopErr
	}
	return err
}

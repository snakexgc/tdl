package application

import (
	"context"
	"fmt"

	"github.com/snakexgc/tdl/application/forwarder"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

type ForwardQueue = forwarder.Queue

func NewForwardQueue(store ports.ForwardRepository) *ForwardQueue { return forwarder.NewQueue(store) }

// ServeForwardQueue attaches a live connection to the account queue and drains
// its RTE runnable before returning ownership of that connection to the caller.
func ServeForwardQueue(ctx context.Context, account types.AccountID, queue *ForwardQueue, transport ports.ForwardTransport, observe func(*rte.Runtime), stores ...*config.Store) error {
	registry := rte.NewRegistry()
	completed := make(chan error, 1)
	if err := forwarder.Register(registry, queue, transport, completed); err != nil {
		return err
	}
	if account == "" {
		account = types.DefaultAccount
	}
	values, err := componentValues(ctx, forwarder.ID, stores...)
	if err != nil {
		return err
	}
	host, err := registry.Build(account, nil, values)
	if err != nil {
		return err
	}
	if observe != nil {
		observe(host)
	}
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			_ = host.Stop(context.Background())
			return fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-completed:
	}
	if stopErr := host.Stop(context.Background()); stopErr != nil {
		return stopErr
	}
	return err
}

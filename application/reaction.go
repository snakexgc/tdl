package application

import (
	"context"
	"fmt"

	reaction "github.com/snakexgc/tdl/application/trigger.reaction"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func ReactionPolicy(ctx context.Context, account types.AccountID, download, forward []string) (ports.ReactionTrigger, func(), error) {
	registry, err := Registry()
	if err != nil {
		return nil, nil, err
	}
	if download == nil {
		download = []string{}
	}
	if forward == nil {
		forward = []string{}
	}
	host, err := registry.Build(account, map[string]bool{reaction.ID: true}, map[string]map[string]any{
		reaction.ID: {"download": download, "forward": forward},
	})
	if err != nil {
		return nil, nil, err
	}
	stop := func() { _ = host.Stop(context.Background()) }
	for _, status := range host.Start(ctx) {
		if status.State != rte.Running {
			stop()
			return nil, nil, fmt.Errorf("%s: %s", status.ID, status.Detail)
		}
	}
	value, err := host.Resolve(ports.ReactionTriggerName)
	if err != nil {
		stop()
		return nil, nil, err
	}
	return value.(ports.ReactionTrigger), stop, nil
}

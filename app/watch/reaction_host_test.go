package watch

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	reaction "github.com/snakexgc/tdl/application/trigger.reaction"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const testForwardField = "forward"

func assembleTestReactionPolicy(ctx context.Context, account types.AccountID, download, forward []string) (ports.ReactionTrigger, func(), error) {
	registry, err := application.Registry()
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
		reaction.ID: {"download": download, testForwardField: forward},
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

func testReactionPolicy(t *testing.T, download, forward []string) ports.ReactionTrigger {
	t.Helper()
	port, stop, err := assembleTestReactionPolicy(context.Background(), types.DefaultAccount, download, forward)
	require.NoError(t, err)
	t.Cleanup(stop)
	return port
}

func reactionTestWatcher(t *testing.T, w *Watcher) *Watcher {
	t.Helper()
	if w.opts.Reaction == nil {
		w.opts.Reaction = testReactionPolicy(t, nil, nil)
	}
	return w
}

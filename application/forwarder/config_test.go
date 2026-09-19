package forwarder

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

func TestQueueConfigurationChangesRetryBudgetAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	store := config.NewStore(t.TempDir())
	q := newTestQueue()
	build := func(values map[string]any) *rte.Runtime {
		registry := rte.NewRegistry()
		require.NoError(t, Register(registry, q, transportFunc(func(ctx context.Context, _ *types.ForwardJob, _ func(types.ForwardJob)) error { return ctx.Err() }), nil))
		host, err := registry.Build(types.DefaultAccount, nil, map[string]map[string]any{ID: values})
		require.NoError(t, err)
		require.Equal(t, rte.Running, host.Start(ctx)[0].State)
		t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
		return host
	}
	host := build(nil)
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{"max_attempts": 1, retryBaseField: 2, "retry_max_seconds": 8}, store))
	before := q.policy()
	require.Error(t, host.PatchSaved(ctx, ID, map[string]any{retryBaseField: 9}, store))
	require.Equal(t, before, q.policy())
	require.NoError(t, host.Stop(ctx))
	document, err := store.Load(ctx, ID)
	require.NoError(t, err)
	restarted := build(document.Values)
	require.Equal(t, before, q.policy())
	require.NoError(t, restarted.Stop(ctx))
	id, err := q.EnqueueMessage(ctx, 1, 2, "", "", "", forwardModeDefault, false)
	require.NoError(t, err)
	job, _, err := q.store.Get(ctx, id)
	require.NoError(t, err)
	q.runJob(ctx, transportFunc(func(context.Context, *types.ForwardJob, func(types.ForwardJob)) error {
		return errors.New("send failed")
	}), q.store, job)
	job, _, err = q.store.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, StatusError, job.Status)
	require.Equal(t, 8*time.Second, q.policy().backoff(100))
}

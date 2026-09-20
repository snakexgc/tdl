package bot

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	downloadTestTask    = "task-1"
	downloadTestAccount = "account-a"
)

type downloadControlSpy struct {
	executor string
	request  types.DownloadAction
}

func (s *downloadControlSpy) Tasks(ctx context.Context, executor string) ([]types.DownloadTask, error) {
	s.executor = executor
	return []types.DownloadTask{{ID: downloadTestTask, Status: "paused"}}, ctx.Err()
}

func (s *downloadControlSpy) Control(ctx context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	s.request = request
	return types.DownloadActionResult{Matched: 1, Changed: 1}, ctx.Err()
}

func TestLocalDownloadCommandsUseAccountPort(t *testing.T) {
	spy := &downloadControlSpy{}
	controller := &localDownloadControl{port: spy, account: downloadTestAccount}
	factory := func() *localDownloadControl { return controller }
	items, err := runLocalDownloadList(context.Background(), factory)
	require.NoError(t, err)
	require.Equal(t, downloadTestTask, items[0].ID)
	require.Equal(t, config.DownloadExecutorLocal, spy.executor)
	for _, action := range []struct {
		name string
		call func(context.Context, []string) (types.DownloadActionResult, error)
	}{{"pause", controller.Pause}, {"resume", controller.Start}, {"delete", controller.Delete}} {
		result, err := runLocalDownloadAction(context.Background(), factory, func(ctx context.Context, _ *localDownloadControl) (types.DownloadActionResult, error) {
			return action.call(ctx, []string{downloadTestTask})
		})
		require.NoError(t, err)
		require.Equal(t, 1, result.Changed)
		require.Equal(t, types.DownloadAction{Account: downloadTestAccount, Executor: config.DownloadExecutorLocal, Action: action.name, IDs: []string{downloadTestTask}}, spy.request)
	}
}

func TestLocalDownloadCommandsPreserveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	factory := func() *localDownloadControl {
		return &localDownloadControl{port: &downloadControlSpy{}, account: downloadTestAccount}
	}
	_, err := runLocalDownloadList(ctx, factory)
	require.ErrorIs(t, err, context.Canceled)
	_, err = runLocalDownloadAction(ctx, factory, func(ctx context.Context, controller *localDownloadControl) (types.DownloadActionResult, error) {
		return controller.Pause(ctx, []string{downloadTestTask})
	})
	require.ErrorIs(t, err, context.Canceled)
}

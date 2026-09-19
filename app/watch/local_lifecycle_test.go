package watch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func TestDisabledLocalExecutorCanStartWithoutRestartingConnection(t *testing.T) {
	ctx := context.Background()
	store := rteconfig.NewStore(t.TempDir())
	view, err := rteconfig.New(local.Manifest().Config, nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, local.ID, false, view))
	worker := local.New(localSource{}, newInternalTaskStore(newMemoryTaskStorage()), nil)
	host, executor, err := application.LocalDownloadHost(ctx, types.DefaultAccount, worker, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	request := types.DownloadSubmission{Account: types.DefaultAccount, TaskID: "test", FullPath: "test.file"}
	_, err = executor.Submit(ctx, request)
	require.ErrorIs(t, err, ports.ErrDownloadNotAccepted)
	require.NoError(t, store.Save(ctx, local.ID, true, view))
	require.NoError(t, host.ReconcileSaved(ctx, store))
	_, err = host.Resolve(ports.DownloadExecutorName)
	require.NoError(t, err)
	_, err = executor.Submit(ctx, request)
	require.ErrorContains(t, err, "download source is unavailable", "old facade reaches the newly enabled worker")
	require.NotErrorIs(t, err, ports.ErrDownloadNotAccepted)
	require.NoError(t, store.Save(ctx, local.ID, false, view))
	require.NoError(t, host.ReconcileSaved(ctx, store))
	_, err = executor.Submit(ctx, request)
	require.ErrorIs(t, err, ports.ErrDownloadNotAccepted)
	require.NoError(t, store.Save(ctx, local.ID, true, view))
	require.NoError(t, host.ReconcileSaved(ctx, store))
	_, err = host.Resolve(ports.DownloadExecutorName)
	require.NoError(t, err)
}

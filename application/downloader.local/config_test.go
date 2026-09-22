package local

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestConfigurationChangesLiveScannerAndPersists(t *testing.T) {
	ctx := context.Background()
	repo := newRepository()
	streamed := make(chan struct{}, 1)
	worker := New(source{stream: func(_ context.Context, w io.Writer) error {
		_, err := w.Write([]byte("test"))
		streamed <- struct{}{}
		return err
	}}, repo, nil)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, worker))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(ctx)[0].State)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	store := configtest.NewStore()
	require.NoError(t, host.PatchSaved(ctx, ID, map[string]any{pollIntervalField: 100, shutdownTimeoutField: 9}, store))
	require.Equal(t, 100*time.Millisecond, worker.scanInterval())
	require.Equal(t, 9*time.Second, worker.pauseTimeout())
	require.Error(t, host.PatchSaved(ctx, ID, map[string]any{pollIntervalField: 0}, store))
	require.Equal(t, 100*time.Millisecond, worker.scanInterval())
	path := filepath.Join(t.TempDir(), "pending.bin")
	require.NoError(t, repo.Save(ctx, types.LocalDownloadRecord{ID: testTask, TaskID: testTask, Dir: filepath.Dir(path), Path: path, Status: types.LocalDownloadStatusQueued, Total: 4}))
	select {
	case <-streamed:
	case <-time.After(2 * time.Second):
		t.Fatal("new scan interval was not applied to the live worker")
	}
	document, err := store.Load(ctx, ID)
	require.NoError(t, err)
	view, err := config.New(Manifest().Config, document.Values)
	require.NoError(t, err)
	restarted := New(source{}, newRepository(), nil)
	require.NoError(t, (&service{worker: restarted}).Reconfigure(ctx, view))
	require.Equal(t, worker.scanInterval(), restarted.scanInterval())
	require.Equal(t, worker.pauseTimeout(), restarted.pauseTimeout())
}

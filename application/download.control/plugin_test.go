package downloadcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

type blockingBackend struct{ entered, release chan struct{} }

func (b blockingBackend) ListTasks(ctx context.Context) ([]types.DownloadTask, error) {
	close(b.entered)
	<-ctx.Done()
	<-b.release
	return nil, ctx.Err()
}

func (blockingBackend) ChangeTasks(context.Context, string, []string) (types.DownloadActionResult, error) {
	return types.DownloadActionResult{}, nil
}

func TestStopCancelsAndDrainsDownloadControl(t *testing.T) {
	b := blockingBackend{entered: make(chan struct{}), release: make(chan struct{})}
	r := rte.NewRegistry()
	require.NoError(t, Register(r, map[string]ports.DownloadBackend{localExecutor: b}))
	host, err := r.Build("account", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	port, err := host.Resolve(ports.DownloadControlName)
	require.NoError(t, err)
	control := port.(ports.DownloadControl)
	done := make(chan error, 1)
	go func() { _, err := control.Tasks(context.Background(), "account", localExecutor); done <- err }()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter backend")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.Error(t, host.Stop(ctx))
	_, err = control.Tasks(context.Background(), "account", localExecutor)
	require.ErrorContains(t, err, "stopped")
	close(b.release)
	require.NoError(t, host.Stop(context.Background()))
	require.ErrorIs(t, <-done, context.Canceled)
}

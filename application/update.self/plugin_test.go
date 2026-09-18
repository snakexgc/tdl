package updater

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

func TestComponentStopCancelsAndWaitsForUpdate(t *testing.T) {
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry))
	host, err := registry.Build(types.DefaultAccount, nil, nil)
	require.NoError(t, err)
	host.Start(context.Background())
	value, err := host.Resolve(ports.UpdaterName)
	require.NoError(t, err)
	service := value.(*Service)
	call, done, err := service.begin(context.Background())
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(ctx), context.DeadlineExceeded)
	require.ErrorIs(t, call.Err(), context.Canceled)
	done()
	require.NoError(t, host.Stop(context.Background()))
	_, err = service.Check(context.Background())
	require.ErrorContains(t, err, "stopped")
}

package runtime

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

type unavailableLocalSource struct{}

func (unavailableLocalSource) Get(context.Context, string) (types.LocalDownloadSource, bool, error) {
	return types.LocalDownloadSource{}, false, nil
}

func (unavailableLocalSource) Acquire(context.Context, string, int) (ports.DownloadLease, error) {
	return nil, errors.New("unexpected transfer")
}

func (unavailableLocalSource) Stream(context.Context, string, ports.DownloadLease, int64, int64, io.Writer) error {
	return errors.New("unexpected stream")
}

func TestLocalComponentProductionConfigurationSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverFile, map[string]any{"path": filepath.Join(t.TempDir(), "tasks")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	storage, err := engine.Open("default")
	require.NoError(t, err)
	store := rteconfig.NewStore(t.TempDir())
	worker := local.New(unavailableLocalSource{}, taskhub.NewLocalRepository(storage), nil)
	host, _, err := application.LocalDownloadHost(ctx, types.DefaultAccount, worker, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	m := &Manager{localHost: host, componentStore: store}
	const intervalField = "poll_interval_ms"
	require.NoError(t, m.SaveComponentConfiguration(ctx, local.ID, map[string]any{intervalField: 200}))
	require.Error(t, m.SaveComponentConfiguration(ctx, local.ID, map[string]any{intervalField: -1}))
	before := host.Configurations()[0].Values
	require.NoError(t, host.Stop(ctx))
	restarted, _, err := application.LocalDownloadHost(ctx, types.DefaultAccount, local.New(unavailableLocalSource{}, taskhub.NewLocalRepository(storage), nil), store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Stop(ctx)) })
	require.Equal(t, before, restarted.Configurations()[0].Values)
}

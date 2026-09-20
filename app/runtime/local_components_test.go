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
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/kv"
)

const testStoragePath = "path"

func TestSavedLocalLinksHonorLiveComponentState(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	cfg.Downloader.LocalRoot = t.TempDir()
	cfg.DownloadDir = "P"
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "tasks")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	storage, err := engine.Open(cfg.Namespace)
	require.NoError(t, err)
	const source = `{"id":"document_42","peer_id":12345,"file_name":"video.mp4","file_size":100,"media":{"name":"video.mp4","size":100,"dc":2,"location":{"kind":"document","id":42,"access_hash":99}}}`
	require.NoError(t, storage.Set(ctx, taskhub.LinkPrefix+"document_42", []byte(source)))
	store := newStoppedComponentStore(t)
	saveComponent(t, store, "download.control", true, map[string]any{"local_root": cfg.Downloader.LocalRoot})
	saveComponent(t, store, "naming.rules", true, map[string]any{fieldDirectory: cfg.DownloadDir})
	m := NewManager(config.WithSource(ctx, config.NewSource(cfg)), engine, storage, Options{ComponentStore: store})
	t.Cleanup(m.Shutdown)
	require.NoError(t, m.configurationErr)
	executor := savedLocalLinks{manager: m}
	request := types.DownloadSubmission{Account: m.downloadAccount, TaskID: "document_42"}
	for _, id := range []string{ports.NamingRulesName, local.ID} {
		require.NoError(t, m.SetComponentEnabled(ctx, id, false, ""))
		m.transitionWG.Wait()
		_, err := executor.Submit(ctx, request)
		require.Error(t, err, "disabled %s must not be recreated from legacy config", id)
		records, err := taskhub.NewLocalRepository(storage).Records(ctx)
		require.NoError(t, err)
		require.Empty(t, records)
		require.NoError(t, m.SetComponentEnabled(ctx, id, true, ""))
		m.transitionWG.Wait()
	}
	require.NoError(t, m.SaveComponentConfiguration(ctx, ports.NamingRulesName, map[string]any{fieldDirectory: "saved/P"}))
	m.transitionWG.Wait()
	_, err = executor.Submit(ctx, request)
	require.NoError(t, err)
	record, exists, err := taskhub.NewLocalRepository(storage).Get(ctx, request.TaskID)
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, filepath.Join(cfg.Downloader.LocalRoot, "saved", "12345", "video.mp4"), filepath.FromSlash(record.Path))
}

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
	engine, err := kv.New(kv.DriverFile, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "tasks")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	storage, err := engine.Open("default")
	require.NoError(t, err)
	store := newComponentStore(t)
	worker := local.New(unavailableLocalSource{}, taskhub.NewLocalRepository(storage), nil)
	host, _, err := application.LocalDownloadHost(ctx, types.DefaultAccount, worker, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	m := &Manager{localHost: host, componentStore: store}
	require.NoError(t, m.initDirectory())
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

package integration_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	aria2 "github.com/snakexgc/tdl/application/downloader.aria2"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

type observationClient struct{ ports.Aria2ControlClient }

func (observationClient) TellActive(context.Context) ([]types.Aria2DownloadStatus, error) {
	return nil, nil
}

func (observationClient) TellWaiting(context.Context, int, int) ([]types.Aria2DownloadStatus, error) {
	return nil, nil
}

func (observationClient) TellStopped(context.Context, int, int) ([]types.Aria2DownloadStatus, error) {
	return []types.Aria2DownloadStatus{
		{GID: "retried", Status: string(types.DownloadComplete), TotalLength: "42", CompletedLength: "42", Files: []types.Aria2File{{Path: "/downloads/movie.mp4", URIs: []types.Aria2URI{{URI: "https://downloads.test/prefix/download/source"}}}}},
		{GID: "unrelated", Status: string(types.DownloadComplete), Files: []types.Aria2File{{URIs: []types.Aria2URI{{URI: "https://foreign.test/prefix/download/source"}}}}},
	}, nil
}

func TestAria2ObservationMaintainsLegacyLinkWithoutPanel(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "store")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open(testIsolatedAccount)
	require.NoError(t, err)
	// Deliberately unindexed legacy link; no panel or watcher is instantiated.
	require.NoError(t, store.Set(ctx, taskhub.LinkPrefix+testLinkSource, []byte(`{"id":"source","file_name":"movie.mp4"}`)))
	repo := taskhub.Aria2Observations{Links: taskhub.LinkRepository{Store: store, Engine: engine, Namespace: testIsolatedAccount}}
	observer := aria2.Observer{Client: observationClient{}, Repository: repo, PublicBaseURL: "https://downloads.test/prefix", TTL: time.Hour}
	require.NoError(t, observer.Sync(ctx))
	snapshot, err := repo.Snapshot(ctx)
	require.NoError(t, err)
	require.True(t, snapshot.Links[testLinkSource].Task.Downloaded)
	require.Len(t, snapshot.Records, 1)
	require.Equal(t, "/downloads", snapshot.Records["retried"].Dir)
	require.NoError(t, observer.Sync(ctx))
	snapshot, err = repo.Snapshot(ctx)
	require.NoError(t, err)
	require.Len(t, snapshot.Records, 1)
}

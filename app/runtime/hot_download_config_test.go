package runtime

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte"
)

func TestDownloadMetadataDoesNotRestartTransports(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Modules = config.ModulesConfig{}
	ctx := config.WithSource(context.Background(), config.NewSource(cfg))
	manager := NewManager(ctx, nil, nil, Options{})
	t.Cleanup(manager.Shutdown)
	find := func(units []rte.ManagedUnit, id string) rte.ManagedUnit {
		for _, unit := range units {
			if unit.ID == id {
				return unit
			}
		}
		t.Fatalf("missing resource %s", id)
		return rte.ManagedUnit{}
	}
	before, original := manager.managedUnits(cfg), manager.aria2Mgr
	next := *cfg
	next.Aria2.Dir = "/another/remote/directory"
	next.HTTP.PublicBaseURL = "https://new.example"
	next.HTTP.DownloadLinkTTLHours = 72
	next.Downloader.Mode = config.DownloaderModeLocal
	after := manager.managedUnits(&next)
	for _, id := range []string{"http", moduleIDAria2, "watch", downloadResource} {
		require.Equal(t, find(before, id).Revision, find(after, id).Revision, id)
	}
	require.NoError(t, find(after, moduleIDAria2).Update(ctx))
	require.Same(t, original, manager.aria2Mgr)
	next.Aria2.RPCURL = "http://127.0.0.1:16800/jsonrpc"
	require.NotEqual(t, find(after, moduleIDAria2).Revision, find(manager.managedUnits(&next), moduleIDAria2).Revision)
}

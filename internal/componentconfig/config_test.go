package componentconfig

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	legacy "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

func TestExportAndLoadOwnEveryAdapterSetting(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	original := legacy.DefaultConfig()
	original.Namespace = "alice"
	original.Debug = true
	original.Bot.Token = "bot-secret"
	original.Bot.Proxy = "http://bot:secret@localhost:8888"
	original.Bot.AllowedUsers = []int64{123}
	original.Aria2.RPCURL = "http://localhost:7001/jsonrpc"
	original.Aria2.Secret = "rpc-secret"
	original.Aria2.Dir = "/remote"
	original.HTTP.Port = 31111
	original.WebUI.Port = 31112
	original.WebUI.Password = "panel-secret"
	original.Forward.Target = "123"
	original.Forward.Listen = []string{"channel"}
	original.Modules.Forward = true
	original.Modules.Bot = false
	original.PoolSize = 4
	original.Limit = 2
	original.NTP = "time.example.org"
	documents, err := Export(original, catalog)
	require.NoError(t, err)
	store := config.NewStore(t.TempDir())
	for id, document := range documents {
		view, err := catalog.View(ctx, id, document.Values)
		require.NoError(t, err, id)
		require.NoError(t, store.Save(ctx, id, document.Enabled, view))
	}
	bootstrap := legacy.DefaultConfig()
	bootstrap.Namespace = original.Namespace
	bootstrap.Debug = original.Debug
	bootstrap.Bot.Token = legacyPoison
	bootstrap.Aria2.Secret = legacyPoison
	bootstrap.Forward.Target = legacyPoison
	bootstrap.PoolSize = 99
	loaded, enabled, err := Load(ctx, store, bootstrap)
	require.NoError(t, err)
	require.Equal(t, original, loaded)
	require.False(t, enabled["console.bot"])
	require.True(t, enabled["trigger.forward"])
	require.Equal(t, legacyPoison, bootstrap.Bot.Token, "loading cannot mutate the source")
}

func TestMissingDocumentsUseDefaultsAndSnapshotsAreIsolated(t *testing.T) {
	bootstrap := legacy.DefaultConfig()
	bootstrap.Aria2.Secret = "old-secret"
	bootstrap.Bot.AllowedUsers = []int64{777}
	loaded, _, err := Load(context.Background(), config.NewStore(t.TempDir()), bootstrap)
	require.NoError(t, err)
	require.Empty(t, loaded.Aria2.Secret)
	require.Empty(t, loaded.Bot.AllowedUsers)
	source := legacy.NewSource(loaded)
	ctx := legacy.WithSource(context.Background(), source)
	first := legacy.From(ctx)
	first.Aria2.Secret = "mutated"
	require.Empty(t, legacy.From(ctx).Aria2.Secret)
	loaded.Aria2.Secret = "updated"
	source.Replace(loaded)
	require.Equal(t, "updated", legacy.From(ctx).Aria2.Secret)
}

const legacyPoison = "wrong"

package componentconfig

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	manager "github.com/snakexgc/tdl/application/configuration.manager"
	runtimeconfig "github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/rte/config"
)

func TestComponentConfigurationOwnsEveryAdapterSetting(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	original := runtimeconfig.DefaultConfig()
	original.Namespace = "alice"
	original.Debug = true
	original.Bot.Token = "bot-secret"
	original.Proxy = "http://shared:secret@localhost:8888"
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
	store := newComponentStore(t)
	inputs := map[string]manager.Component{
		accountComponentID: {Enabled: true, Values: map[string]any{"proxy": original.Proxy, "file_limit": 2, "dc_pool_size": 4, ntpField: "time.example.org"}},
		consoleComponentID: {Enabled: false, Values: map[string]any{"token": "bot-secret", "allowed_users": []string{"123"}}},
		"downloader.aria2": {Enabled: true, Values: map[string]any{"rpc_url": "http://localhost:7001/jsonrpc", "secret": "rpc-secret", directoryField: "/remote"}},
		"proxy.range":      {Enabled: true, Values: map[string]any{portField: 31111}},
		"panel.webui":      {Enabled: true, Values: map[string]any{portField: 31112, "password": "panel-secret"}},
		"forwarder":        {Enabled: true, Values: map[string]any{"target": "123"}},
		"trigger.forward":  {Enabled: true, Values: map[string]any{"listen": []string{"channel"}}},
	}
	for id, input := range inputs {
		view, err := catalog.View(ctx, id, input.Values)
		require.NoError(t, err)
		require.NoError(t, store.Save(ctx, id, input.Enabled, view))
	}
	bootstrap := runtimeconfig.DefaultConfig()
	bootstrap.Namespace = original.Namespace
	bootstrap.Debug = original.Debug
	bootstrap.Bot.Token = staleValue
	bootstrap.Aria2.Secret = staleValue
	bootstrap.Forward.Target = staleValue
	bootstrap.PoolSize = 99
	loaded, enabled, err := Load(ctx, store, bootstrap)
	require.NoError(t, err)
	require.Equal(t, original, loaded)
	require.False(t, enabled[consoleComponentID])
	require.True(t, enabled["trigger.forward"])
	require.Equal(t, staleValue, bootstrap.Bot.Token, "loading cannot mutate the source")
}

func TestMissingDocumentsUseDefaultsAndSnapshotsAreIsolated(t *testing.T) {
	bootstrap := runtimeconfig.DefaultConfig()
	bootstrap.Aria2.Secret = "old-secret"
	bootstrap.Bot.AllowedUsers = []int64{777}
	loaded, _, err := Load(context.Background(), newComponentStore(t), bootstrap)
	require.NoError(t, err)
	require.Empty(t, loaded.Aria2.Secret)
	require.Empty(t, loaded.Bot.AllowedUsers)
	source := runtimeconfig.NewSource(loaded)
	ctx := runtimeconfig.WithSource(context.Background(), source)
	first := runtimeconfig.From(ctx)
	first.Aria2.Secret = "mutated"
	require.Empty(t, runtimeconfig.From(ctx).Aria2.Secret)
	loaded.Aria2.Secret = "updated"
	source.Replace(loaded)
	require.Equal(t, "updated", runtimeconfig.From(ctx).Aria2.Secret)
}

const staleValue = "wrong"

func TestPerServiceProxyOverridesAreRejected(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	for _, id := range []string{consoleComponentID, "update.self"} {
		_, err := catalog.View(ctx, id, map[string]any{proxyField: "http://old:secret@127.0.0.1:8000"})
		require.Error(t, err, id)
	}
	store := newComponentStore(t)
	view, err := catalog.View(ctx, accountComponentID, map[string]any{proxyField: "socks5://shared:secret@127.0.0.1:1080"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, accountComponentID, true, view))
	loaded, _, err := Load(ctx, store, runtimeconfig.DefaultConfig())
	require.NoError(t, err)
	require.Equal(t, "socks5://shared:secret@127.0.0.1:1080", loaded.Proxy)
	view, err = catalog.View(ctx, accountComponentID, nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, accountComponentID, true, view))
	loaded, _, err = Load(ctx, store, loaded)
	require.NoError(t, err)
	require.Empty(t, loaded.Proxy)
}

func newComponentStore(t *testing.T) *config.Store {
	t.Helper()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	service := manager.New(config.File{Path: filepath.Join(t.TempDir(), manager.Filename)}, catalog)
	doc, err := service.Defaults(context.Background())
	require.NoError(t, err)
	require.NoError(t, service.Create(context.Background(), doc))
	return service.Store()
}

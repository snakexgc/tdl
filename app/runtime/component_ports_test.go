package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestStoredDaemonComponentsReachProductionPorts(t *testing.T) {
	ctx := context.Background()
	store := newComponentStore(t)
	saveComponent(t, store, "account.telegram", true, map[string]any{"api_id": 12345, "api_hash": "configured-secret", fieldUseBuiltin: false})
	saveComponent(t, store, "trigger.reaction", true, map[string]any{fieldDownloadReaction: []string{"🔥"}})
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	m := &Manager{policies: host, filter: filter, naming: naming, componentStore: store, savedStore: store}
	require.NoError(t, m.initDirectory())
	opts := m.watchOptions(cfg)
	require.NotNil(t, opts.Reaction)
	require.NotNil(t, opts.MessageLinks)
	account := types.AccountID(cfg.Namespace)
	credentials, err := opts.Credentials.Resolve(ctx, account)
	require.NoError(t, err)
	require.Equal(t, 12345, credentials.App.AppID)
	require.Equal(t, "configured-secret", credentials.App.AppHash)
	in := ports.ReactionInput{Account: account, Reactions: []ports.Reaction{{Mine: true, Value: "🔥"}}}
	require.True(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, m.SaveComponentConfiguration(ctx, "trigger.reaction", map[string]any{fieldDownloadReaction: []string{"👍"}}))
	require.True(t, opts.Reaction.Matches(ctx, in), "saved edits must not change the running watcher")
	in.Reactions[0].Value = "👍"
	require.False(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, m.SaveComponentConfiguration(ctx, "account.telegram", map[string]any{apiIDField: 54321, apiHashField: ""}))
	require.ErrorContains(t, m.SaveComponentConfiguration(ctx, "update.self", map[string]any{"proxy": "http://user:password@127.0.0.1:8080"}), "undeclared")
	require.NoError(t, m.SaveComponentConfiguration(ctx, "account.telegram", map[string]any{"proxy": "http://user:password@127.0.0.1:8080"}))
	entries, editable := m.ComponentConfigurations()
	require.True(t, editable)
	require.Len(t, entries, 19)
	encoded, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "configured-secret")
	require.NotContains(t, string(encoded), "user:password")
	for _, entry := range entries {
		require.NotNil(t, entry.Fields)
	}
	require.NoError(t, host.Stop(ctx))
	restarted, _, _, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, restarted.Stop(ctx)) }()
	m.policies = restarted
	credentials, err = m.Resolve(ctx, account)
	require.NoError(t, err)
	require.Equal(t, 54321, credentials.App.AppID)
	require.Equal(t, "configured-secret", credentials.App.AppHash)
	for _, entry := range restarted.Configurations() {
		if entry.ID == "trigger.reaction" {
			require.Equal(t, []any{"👍"}, entry.Values[fieldDownloadReaction])
		}
	}
}

func TestUnavailableProductionPortsDoNotFallBack(t *testing.T) {
	m := &Manager{}
	require.NoError(t, m.initDirectory())
	_, err := m.Resolve(context.Background(), types.DefaultAccount)
	require.Error(t, err)
	_, err = m.Check(context.Background())
	require.Error(t, err)
	_, _, err = m.Download(context.Background())
	require.Error(t, err)
}

func TestDisabledFilterPreservesIndependentAccountPort(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	store := newComponentStore(t)
	view, err := catalog.View(ctx, "filter.rules", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "filter.rules", false, view))
	host, filter, naming, err := newPolicyHostStored(ctx, config.DefaultConfig(), store)
	require.Error(t, err)
	require.NotNil(t, host)
	defer func() { require.NoError(t, host.Stop(ctx)) }()
	require.Nil(t, filter)
	require.NotNil(t, naming)
	_, err = host.Resolve(ports.TelegramCredentialsName)
	require.NoError(t, err)
}

func TestDisabledProxyProviderDoesNotPreventIndependentPoliciesAndCanRecover(t *testing.T) {
	ctx := context.Background()
	catalog, err := application.Catalog()
	require.NoError(t, err)
	store := newComponentStore(t)
	view, err := catalog.View(ctx, "account.telegram", nil)
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "account.telegram", false, view))
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	require.NotNil(t, filter)
	require.NotNil(t, naming)
	_, err = host.Resolve(ports.UpdaterName)
	require.Error(t, err, "updating cannot silently bypass the unavailable shared proxy")
	require.NoError(t, store.Save(ctx, "account.telegram", true, view))
	desired, err := buildCatalogPolicyHost(ctx, cfg, store, catalog)
	require.NoError(t, err)
	require.NoError(t, host.ReconcileComponents(ctx, desired))
	_, err = host.Resolve(ports.UpdaterName)
	require.NoError(t, err)
}

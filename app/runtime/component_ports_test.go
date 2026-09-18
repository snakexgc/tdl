package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/migration"
	"github.com/snakexgc/tdl/pkg/config"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func TestStoredDaemonComponentsReachProductionPorts(t *testing.T) {
	ctx := context.Background()
	plan, err := migration.Prepare(strings.NewReader(`{"telegram":{"api_id":12345,"api_hash":"imported-secret"},"trigger_reactions":["🔥"]}`))
	require.NoError(t, err)
	directory := filepath.Join(t.TempDir(), "components")
	require.NoError(t, plan.Write(ctx, directory))
	store := rteconfig.NewStore(directory)
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	m := &Manager{policies: host, filter: filter, naming: naming, componentStore: store}
	opts := m.watchOptions(cfg)
	require.NotNil(t, opts.Reaction)
	require.NotNil(t, opts.MessageLinks)
	account := types.AccountID(cfg.Namespace)
	credentials, err := opts.Credentials.Resolve(ctx, account, "desktop")
	require.NoError(t, err)
	require.Equal(t, 12345, credentials.App.AppID)
	require.Equal(t, "imported-secret", credentials.App.AppHash)
	in := ports.ReactionInput{Account: account, Reactions: []ports.Reaction{{Mine: true, Value: "🔥"}}}
	require.True(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, m.SaveComponentConfiguration(ctx, "trigger.reaction", map[string]any{"download": []string{"👍"}}))
	require.False(t, opts.Reaction.Matches(ctx, in), "existing watcher sees the same port's new configuration")
	in.Reactions[0].Value = "👍"
	require.True(t, opts.Reaction.Matches(ctx, in))
	require.NoError(t, m.SaveComponentConfiguration(ctx, "account.telegram", map[string]any{"api_id": 54321, "api_hash": ""}))
	require.NoError(t, m.SaveComponentConfiguration(ctx, "update.self", map[string]any{"proxy": "http://user:password@127.0.0.1:8080"}))
	entries, editable := m.ComponentConfigurations()
	require.True(t, editable)
	require.Len(t, entries, 6)
	encoded, err := json.Marshal(entries)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "imported-secret")
	require.NotContains(t, string(encoded), "user:password")
	for _, entry := range entries {
		require.NotNil(t, entry.Fields)
	}
	require.NoError(t, host.Stop(ctx))
	restarted, _, _, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	defer func() { require.NoError(t, restarted.Stop(ctx)) }()
	m.policies = restarted
	credentials, err = m.Resolve(ctx, account, "desktop")
	require.NoError(t, err)
	require.Equal(t, 54321, credentials.App.AppID)
	require.Equal(t, "imported-secret", credentials.App.AppHash)
	for _, entry := range restarted.Configurations() {
		if entry.ID == "trigger.reaction" {
			require.Equal(t, []any{"👍"}, entry.Values["download"])
		}
	}
}

func TestUnavailableProductionPortsDoNotFallBack(t *testing.T) {
	m := &Manager{}
	_, err := m.Resolve(context.Background(), types.DefaultAccount, "builtin")
	require.Error(t, err)
	_, err = m.Check(context.Background())
	require.Error(t, err)
	_, _, err = m.Download(context.Background())
	require.Error(t, err)
}

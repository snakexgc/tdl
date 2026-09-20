package webui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

type testLoginCredentials struct{ account types.AccountID }

func (c *testLoginCredentials) Resolve(_ context.Context, account types.AccountID) (types.TelegramCredentials, error) {
	c.account = account
	return types.TelegramCredentials{Preset: "desktop"}, nil
}

func TestLoginKeepsInitialAccountWhileConfigurationChanges(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Namespace = "alice"
	source := config.NewSource(cfg)
	ctx := config.WithSource(context.Background(), source)
	manager := newWebLoginManager(Options{Context: ctx})
	defer manager.Stop(context.Background())
	cfg.Namespace = "bob"
	source.Replace(cfg)
	require.Equal(t, "alice", manager.currentNamespace(), "a pending login must retain its expected account")
}

func TestLoginCredentialsBindSelectedNamespace(t *testing.T) {
	profile := &testLoginCredentials{}
	m := newWebLoginManager(Options{Namespace: "alice", Credentials: profile})
	defer func() { require.NoError(t, m.Stop(context.Background())) }()
	opts := m.sessionOptions("bob", nil)
	require.Equal(t, types.AccountID("bob"), opts.Account)
	credentials, err := opts.Credentials.Resolve(context.Background(), opts.Account)
	require.NoError(t, err)
	require.Equal(t, "desktop", credentials.Preset)
	require.Equal(t, types.AccountID("alice"), profile.account)
	_, err = opts.Credentials.Resolve(context.Background(), "eve")
	require.ErrorContains(t, err, "account mismatch")
}

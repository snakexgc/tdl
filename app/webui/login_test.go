package webui

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
)

type testLoginCredentials struct{ account types.AccountID }

func (c *testLoginCredentials) Resolve(_ context.Context, account types.AccountID, preset string) (types.TelegramCredentials, error) {
	c.account = account
	return types.TelegramCredentials{Preset: preset}, nil
}

func TestLoginCredentialsBindSelectedNamespace(t *testing.T) {
	profile := &testLoginCredentials{}
	m := newWebLoginManager(Options{Namespace: "alice", Credentials: profile})
	defer func() { require.NoError(t, m.Stop(context.Background())) }()
	opts := m.sessionOptions("bob", nil)
	require.Equal(t, types.AccountID("bob"), opts.Account)
	credentials, err := opts.Credentials.Resolve(context.Background(), opts.Account, "desktop")
	require.NoError(t, err)
	require.Equal(t, "desktop", credentials.Preset)
	require.Equal(t, types.AccountID("alice"), profile.account)
	_, err = opts.Credentials.Resolve(context.Background(), "eve", "desktop")
	require.ErrorContains(t, err, "account mismatch")
}

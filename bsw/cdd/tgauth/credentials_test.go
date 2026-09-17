package tgauth

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestCredentialChangePreservesSessionAndCanBeReverted(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{"path": filepath.Join(t.TempDir(), "db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("account")
	require.NoError(t, err)
	legacy := types.TelegramApp{AppID: 1, AppHash: "original"}
	custom := types.TelegramApp{AppID: 2, AppHash: "custom"}
	session := []byte("existing-session")
	require.NoError(t, store.Set(ctx, "session", session))
	require.NoError(t, ValidateCredentials(ctx, store, legacy, legacy))
	require.ErrorIs(t, ValidateCredentials(ctx, store, custom, legacy), ErrNeedsRelogin)
	data, err := store.Get(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, session, data)
	require.NoError(t, ValidateCredentials(ctx, store, legacy, legacy))
	require.NoError(t, CommitSession(ctx, store, []byte("new-session"), "desktop", Fingerprint(custom)))
	require.NoError(t, ValidateCredentials(ctx, store, custom, legacy))
	require.ErrorIs(t, ValidateCredentials(ctx, store, legacy, legacy), ErrNeedsRelogin)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, CommitSession(cancelled, store, session, "builtin", Fingerprint(legacy)), context.Canceled)
	data, err = store.Get(ctx, "session")
	require.NoError(t, err)
	require.Equal(t, "new-session", string(data))
}

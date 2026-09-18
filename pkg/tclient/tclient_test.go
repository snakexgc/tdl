package tclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type credentialResolver func(context.Context, types.AccountID, string) (types.TelegramCredentials, error)

func (f credentialResolver) Resolve(ctx context.Context, account types.AccountID, preset string) (types.TelegramCredentials, error) {
	return f(ctx, account, preset)
}

func TestResolveAppUsesInjectedCredentialsWithoutFallback(t *testing.T) {
	store := appStorage{get: func(context.Context, string) ([]byte, error) { return []byte(AppDesktop), nil }}
	want := types.TelegramCredentials{App: types.TelegramApp{AppID: 123, AppHash: "component-secret"}, Preset: AppDesktop}
	service := credentialResolver(func(_ context.Context, account types.AccountID, preset string) (types.TelegramCredentials, error) {
		require.Equal(t, types.AccountID("production"), account)
		require.Equal(t, AppDesktop, preset)
		return want, nil
	})
	got, err := ResolveAppUsing(context.Background(), store, "production", service)
	require.NoError(t, err)
	require.Equal(t, want, got)
	unavailable := errors.New("host unavailable")
	_, err = ResolveAppUsing(context.Background(), store, "production", credentialResolver(func(context.Context, types.AccountID, string) (types.TelegramCredentials, error) {
		return types.TelegramCredentials{}, unavailable
	}))
	require.ErrorIs(t, err, unavailable)
}

type appStorage struct {
	storage.Storage
	get func(context.Context, string) ([]byte, error)
}

func (s appStorage) Get(ctx context.Context, key string) ([]byte, error) { return s.get(ctx, key) }

func TestGetAppOnlyFallsBackForMissingKey(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := GetApp(ctx, appStorage{get: func(received context.Context, _ string) ([]byte, error) {
		require.Equal(t, ctx, received)
		return nil, received.Err()
	}})
	require.ErrorIs(t, err, context.Canceled)
	broken := errors.New("disk error")
	_, err = GetApp(context.Background(), appStorage{get: func(context.Context, string) ([]byte, error) { return nil, broken }})
	require.ErrorIs(t, err, broken)
	app, err := GetApp(context.Background(), appStorage{get: func(context.Context, string) ([]byte, error) { return nil, storage.ErrNotFound }})
	require.NoError(t, err)
	require.Equal(t, Apps[AppBuiltin], app)
}

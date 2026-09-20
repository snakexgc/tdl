package tclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
)

type credentialResolver func(context.Context, types.AccountID) (types.TelegramCredentials, error)

func (f credentialResolver) Resolve(ctx context.Context, account types.AccountID) (types.TelegramCredentials, error) {
	return f(ctx, account)
}

func TestResolveAppRequiresAccountCredentials(t *testing.T) {
	want := types.TelegramCredentials{App: types.TelegramApp{AppID: 123, AppHash: "component-secret"}, Preset: "desktop"}
	service := credentialResolver(func(_ context.Context, account types.AccountID) (types.TelegramCredentials, error) {
		require.Equal(t, types.AccountID("production"), account)
		return want, nil
	})
	got, err := ResolveApp(context.Background(), "production", service)
	require.NoError(t, err)
	require.Equal(t, want, got)
	_, err = ResolveApp(context.Background(), "production", nil)
	require.Error(t, err)
	unavailable := errors.New("host unavailable")
	_, err = ResolveApp(context.Background(), "production", credentialResolver(func(context.Context, types.AccountID) (types.TelegramCredentials, error) {
		return types.TelegramCredentials{}, unavailable
	}))
	require.ErrorIs(t, err, unavailable)
}

type appStorage struct {
	storage.Storage
	get func(context.Context, string) ([]byte, error)
}

func (s appStorage) Get(ctx context.Context, key string) ([]byte, error) { return s.get(ctx, key) }

func TestGetAppChecksStoredIdentityAndPropagatesStorageErrors(t *testing.T) {
	selected := types.TelegramCredentials{App: types.TelegramApp{AppID: 123, AppHash: "current-hash"}, Preset: "desktop"}
	service := credentialResolver(func(context.Context, types.AccountID) (types.TelegramCredentials, error) { return selected, nil })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := getAppUsing(ctx, nil, types.DefaultAccount, service)
	require.ErrorIs(t, err, context.Canceled)
	broken := errors.New("disk error")
	_, err = getAppUsing(context.Background(), appStorage{get: func(context.Context, string) ([]byte, error) { return nil, broken }}, types.DefaultAccount, service)
	require.ErrorIs(t, err, broken)
	app, err := getAppUsing(context.Background(), appStorage{get: func(context.Context, string) ([]byte, error) { return nil, storage.ErrNotFound }}, types.DefaultAccount, service)
	require.NoError(t, err)
	require.Equal(t, selected.App, app)
	for _, fingerprint := range []string{"", "wrong", tgauth.Fingerprint(selected.App)} {
		app, err = getAppUsing(context.Background(), appStorage{get: func(_ context.Context, key string) ([]byte, error) {
			if key == tgauth.SessionKey {
				return []byte("session"), nil
			}
			if fingerprint == "" {
				return nil, storage.ErrNotFound
			}
			return []byte(fingerprint), nil
		}}, types.DefaultAccount, service)
		if fingerprint == tgauth.Fingerprint(selected.App) {
			require.NoError(t, err)
			require.Equal(t, selected.App, app)
		} else {
			require.ErrorIs(t, err, tgauth.ErrNeedsRelogin)
		}
	}
}

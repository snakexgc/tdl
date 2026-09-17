package tclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
)

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

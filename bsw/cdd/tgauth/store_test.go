package tgauth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestSessionDatasetAndAtomicDeletion(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("account")
	require.NoError(t, err)
	require.NoError(t, CommitSession(ctx, store, []byte("session-data"), "desktop", "identity"))
	require.NoError(t, store.Set(ctx, "session.backup", []byte("preserve")))
	scoped, err := SessionStore(store)
	require.NoError(t, err)
	require.Error(t, scoped.Set(ctx, "session.backup", nil))
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = DeleteSession(canceled, store)
	require.ErrorIs(t, err, context.Canceled)
	count, err := DeleteSession(ctx, store)
	require.NoError(t, err)
	require.Equal(t, 3, count)
	for _, key := range []string{SessionKey, AppKey, FingerprintKey} {
		_, err = store.Get(ctx, key)
		require.ErrorIs(t, err, storage.ErrNotFound)
	}
	value, err := store.Get(ctx, "session.backup")
	require.NoError(t, err)
	require.Equal(t, "preserve", string(value))
	count, err = DeleteSession(ctx, store)
	require.NoError(t, err)
	require.Zero(t, count)
}

type failedDeleteStore struct{ storage.Storage }

func (s failedDeleteStore) Update(ctx context.Context, fn func(storage.Storage) error) error {
	return storage.Update(ctx, s.Storage, func(tx storage.Storage) error { return fn(failedDeleteTx{tx}) })
}

type failedDeleteTx struct{ storage.Storage }

func (s failedDeleteTx) Delete(ctx context.Context, key string) error {
	if key == AppKey {
		return errors.New("delete failed")
	}
	return s.Storage.Delete(ctx, key)
}

func TestSessionDeletionFailureRollsBack(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("account")
	require.NoError(t, err)
	require.NoError(t, CommitSession(ctx, store, []byte("original"), "desktop", "identity"))
	count, err := DeleteSession(ctx, failedDeleteStore{store})
	require.ErrorContains(t, err, "delete failed")
	require.Zero(t, count)
	for _, key := range []string{SessionKey, AppKey, FingerprintKey} {
		_, err = store.Get(ctx, key)
		require.NoError(t, err)
	}
}

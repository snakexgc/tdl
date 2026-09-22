package taskhub_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/bsw/cdd/tgauth"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestCleanupProtectsCredentialsAndConcurrentWrites(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "cleanup"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("maintenance")
	require.NoError(t, err)
	protected := []string{tgauth.SessionKey, tgauth.AppKey, tgauth.FingerprintKey, "peers:42", "state:42", "chan:42", "access_hash:42"}
	for _, key := range protected {
		require.NoError(t, store.Set(ctx, key, []byte("secret")))
	}
	require.NoError(t, store.Set(ctx, "ordinary", []byte("before")))
	repo := taskhub.CleanupRepository{Engine: engine, Namespace: "maintenance", Store: store}
	snapshot, err := repo.Snapshot(ctx)
	require.NoError(t, err)
	require.Equal(t, len(protected), snapshot.Protected)
	require.Len(t, snapshot.Records, 1)
	for _, key := range protected {
		deleted, err := repo.DeleteUnchanged(ctx, key, []byte("secret"))
		require.Error(t, err)
		require.False(t, deleted)
		data, err := store.Get(ctx, key)
		require.NoError(t, err)
		require.Equal(t, "secret", string(data))
	}
	require.NoError(t, store.Set(ctx, "ordinary", []byte("after")))
	deleted, err := repo.DeleteUnchanged(ctx, "ordinary", snapshot.Records["ordinary"])
	require.NoError(t, err)
	require.False(t, deleted)
	data, err := store.Get(ctx, "ordinary")
	require.NoError(t, err)
	require.Equal(t, "after", string(data))
}

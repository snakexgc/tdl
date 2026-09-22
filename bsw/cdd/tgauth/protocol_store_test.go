package tgauth

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/peers"
	"github.com/gotd/td/telegram/updates"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestProtocolDatasetsPersistAndIsolateAccounts(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	a, err := engine.Open("a")
	require.NoError(t, err)
	b, err := engine.Open("b")
	require.NoError(t, err)
	protocol, err := ProtocolStore(a)
	require.NoError(t, err)
	for _, key := range []string{SessionKey, AppKey, "peer", "state", "tasks.job"} {
		require.Error(t, protocol.Set(ctx, key, nil))
	}
	err = storage.Update(ctx, protocol, func(tx storage.Storage) error {
		if err := tx.Set(ctx, "state:1", []byte("rollback")); err != nil {
			return err
		}
		if err := tx.Set(ctx, "chan:1", []byte("rollback")); err != nil {
			return err
		}
		return tx.Set(ctx, SessionKey, nil)
	})
	require.Error(t, err)
	_, err = a.Get(ctx, "state:1")
	require.ErrorIs(t, err, storage.ErrNotFound)
	peerStore, err := PeersStore(a)
	require.NoError(t, err)
	key := peers.Key{Prefix: "user", ID: 5}
	require.NoError(t, peerStore.Save(ctx, key, peers.Value{AccessHash: 99}))
	peerStore, err = PeersStore(a)
	require.NoError(t, err)
	value, found, err := peerStore.Find(ctx, key)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 99, value.AccessHash)
	hashes, err := NewAccessHashes(a)
	require.NoError(t, err)
	require.NoError(t, hashes.SetChannelAccessHash(ctx, 1, 2, 33))
	require.NoError(t, hashes.SetUserAccessHash(ctx, 1, 2, 44))
	hashes, err = NewAccessHashes(a)
	require.NoError(t, err)
	hash, found, err := hashes.GetChannelAccessHash(ctx, 1, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 33, hash)
	hash, found, err = hashes.GetUserAccessHash(ctx, 1, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.EqualValues(t, 44, hash)
	_, found, err = hashes.GetChannelAccessHash(ctx, 3, 2)
	require.NoError(t, err)
	require.False(t, found)
	other, err := NewAccessHashes(b)
	require.NoError(t, err)
	_, found, err = other.GetChannelAccessHash(ctx, 1, 2)
	require.NoError(t, err)
	require.False(t, found)
}

func TestUpdateStateMultipleHandlesKeepChannelOffsets(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, filepath.Join(t.TempDir(), "db"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("account")
	require.NoError(t, err)
	first, err := UpdateStore(store)
	require.NoError(t, err)
	require.NoError(t, first.SetState(ctx, 1, updates.State{Pts: 10}))
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			other, err := UpdateStore(store)
			if err != nil {
				t.Error(err)
				return
			}
			if err := other.SetChannelPts(ctx, 1, int64(i), i); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	require.NoError(t, first.SetState(ctx, 1, updates.State{Pts: 11}))
	count := 0
	require.NoError(t, first.ForEachChannels(ctx, 1, func(_ context.Context, id int64, pts int) error {
		count++
		require.EqualValues(t, id, pts)
		return nil
	}))
	require.Equal(t, 32, count)
}

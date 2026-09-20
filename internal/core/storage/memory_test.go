package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMemoryTransactionsRollbackAndOwnValues(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	original := []byte("before")
	require.NoError(t, store.Set(ctx, "item", original))
	original[0] = '!'
	failure := errors.New("failed")
	require.ErrorIs(t, Update(ctx, store, func(tx Storage) error {
		require.NoError(t, tx.Set(ctx, "item", []byte("after")))
		require.NoError(t, tx.Set(ctx, "extra", []byte("extra")))
		return failure
	}), failure)
	value, err := store.Get(ctx, "item")
	require.NoError(t, err)
	require.Equal(t, "before", string(value))
	value[0] = '!'
	_, err = store.Get(ctx, "extra")
	require.ErrorIs(t, err, ErrNotFound)
	canceled, cancel := context.WithCancel(ctx)
	require.ErrorIs(t, Update(canceled, store, func(tx Storage) error {
		require.NoError(t, tx.Delete(canceled, "item"))
		cancel()
		return nil
	}), context.Canceled)
	value, err = store.Get(ctx, "item")
	require.NoError(t, err)
	require.Equal(t, "before", string(value))
}

func TestMemorySerializesReadModifyWrite(t *testing.T) {
	ctx := context.Background()
	store := &Memory{}
	require.NoError(t, store.Set(ctx, "count", []byte{0}))
	var workers sync.WaitGroup
	failures := make(chan error, 50)
	for range 50 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			failures <- Update(ctx, store, func(tx Storage) error {
				value, err := tx.Get(ctx, "count")
				if err != nil {
					return err
				}
				return tx.Set(ctx, "count", []byte{value[0] + 1})
			})
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	value, err := store.Get(ctx, "count")
	require.NoError(t, err)
	require.Equal(t, byte(50), value[0])
}

type nonTransactionalStore struct{ Storage }

func TestUpdateRejectsNonTransactionalStorage(t *testing.T) {
	called := false
	err := Update(context.Background(), nonTransactionalStore{}, func(Storage) error {
		called = true
		return fmt.Errorf("must not write")
	})
	require.ErrorContains(t, err, "atomic updates")
	require.False(t, called)
}

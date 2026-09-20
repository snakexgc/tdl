package taskhub_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

const (
	testPrefix = "download.local.task."
	testIndex  = "download.local.index"
	testRecord = `{"status":"paused","completed":42,"future_field":true}`
)

func TestCollectionDrivers(t *testing.T) {
	for _, driver := range []kv.Driver{kv.DriverBolt, kv.DriverFile} {
		t.Run(string(driver), func(t *testing.T) {
			ctx := context.Background()
			engine, err := kv.New(driver, filepath.Join(t.TempDir(), "tasks"))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			first, err := engine.Open("account-a")
			require.NoError(t, err)
			second, err := engine.Open("account-a")
			require.NoError(t, err)
			other, err := engine.Open("account-b")
			require.NoError(t, err)
			collections := []*taskhub.Collection{
				taskhub.New(first, testPrefix, testIndex),
				taskhub.New(second, testPrefix, testIndex),
			}
			var wg sync.WaitGroup
			errs := make(chan error, 40)
			for i := range 40 {
				wg.Go(func() {
					errs <- collections[i%2].Put(ctx, fmt.Sprint(i), []byte(testRecord), time.Now())
				})
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			records, err := collections[0].Records(ctx)
			require.NoError(t, err)
			require.Len(t, records, 40)
			require.JSONEq(t, testRecord, string(records["0"]))
			isolated, err := taskhub.New(other, testPrefix, testIndex).Records(ctx)
			require.NoError(t, err)
			require.Empty(t, isolated)

			// Dangling index entries do not appear as records.
			require.NoError(t, first.Delete(ctx, testPrefix+"0"))
			records, err = collections[1].Records(ctx)
			require.NoError(t, err)
			require.Len(t, records, 39)
			require.NoError(t, collections[0].Remove(ctx, "1"))
			_, err = collections[1].Get(ctx, "1")
			require.ErrorIs(t, err, storage.ErrNotFound)

			// A corrupt index must not leave a newly written orphan record.
			require.NoError(t, first.Set(ctx, testIndex, []byte("broken")))
			require.Error(t, collections[0].Put(ctx, "orphan", []byte(testRecord), time.Now()))
			_, err = first.Get(ctx, testPrefix+"orphan")
			require.ErrorIs(t, err, storage.ErrNotFound)

			abort := errors.New("abort transaction")
			err = storage.Update(ctx, first, func(tx storage.Storage) error {
				if err := tx.Set(ctx, "rollback", []byte("pending")); err != nil {
					return err
				}
				if err := tx.Delete(ctx, testPrefix+"2"); err != nil {
					return err
				}
				return abort
			})
			require.ErrorIs(t, err, abort)
			_, err = second.Get(ctx, "rollback")
			require.ErrorIs(t, err, storage.ErrNotFound)
			_, err = collections[1].Get(ctx, "2")
			require.NoError(t, err)

			cancelled, cancel := context.WithCancel(ctx)
			err = storage.Update(cancelled, first, func(tx storage.Storage) error {
				if err := tx.Set(ctx, "cancelled", []byte("pending")); err != nil {
					return err
				}
				cancel()
				return nil
			})
			require.ErrorIs(t, err, context.Canceled)
			_, err = second.Get(ctx, "cancelled")
			require.ErrorIs(t, err, storage.ErrNotFound)
		})
	}
}

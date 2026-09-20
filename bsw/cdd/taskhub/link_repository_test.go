package taskhub_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestLinkCatalogOnlyIncludesIndexedAssociations(t *testing.T) {
	for _, driver := range []kv.Driver{kv.DriverBolt, kv.DriverFile} {
		t.Run(string(driver), func(t *testing.T) {
			ctx := context.Background()
			engine, err := kv.New(driver, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "links")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			store, err := engine.Open("links-account")
			require.NoError(t, err)
			repo := taskhub.LinkRepository{Store: store, Engine: engine, Namespace: "links-account"}
			require.NoError(t, taskhub.Links(store).Put(ctx, "source", []byte(`{}`), time.Now()))
			require.NoError(t, taskhub.Aria2(store).Put(ctx, "indexed", []byte(`{"task_id":"source"}`), time.Now()))
			require.NoError(t, store.Set(ctx, taskhub.Aria2Prefix+"legacy", []byte(`{"task_id":"source"}`)))
			require.NoError(t, store.Set(ctx, taskhub.Aria2Prefix+"unrelated", []byte(`{"task_id":"other"}`)))
			require.NoError(t, store.Set(ctx, "session", []byte("private credentials")))
			snapshot, err := repo.Snapshot(ctx)
			require.NoError(t, err)
			require.Len(t, snapshot, 2)
			require.NotContains(t, snapshot, taskhub.Aria2Prefix+"legacy")
			require.NotContains(t, snapshot, "session")
			require.NotContains(t, snapshot, taskhub.LinkIndex)
			require.NotContains(t, snapshot, taskhub.Aria2Index)
			snapshot[taskhub.LinkPrefix+"source"][0] = '!'
			original, err := store.Get(ctx, taskhub.LinkPrefix+"source")
			require.NoError(t, err)
			require.JSONEq(t, `{}`, string(original))
			// An invalid index must abort without deleting any source data.
			savedIndex, err := store.Get(ctx, taskhub.LinkIndex)
			require.NoError(t, err)
			require.NoError(t, store.Set(ctx, taskhub.LinkIndex, []byte("broken")))
			_, err = repo.Remove(ctx, "source")
			require.Error(t, err)
			_, err = store.Get(ctx, taskhub.Aria2Prefix+"legacy")
			require.NoError(t, err)
			require.NoError(t, store.Set(ctx, taskhub.LinkIndex, savedIndex))
			count, err := repo.Remove(ctx, "source")
			require.NoError(t, err)
			require.Equal(t, 2, count)
			for _, key := range []string{taskhub.LinkPrefix + "source", taskhub.Aria2Prefix + "indexed"} {
				_, err = store.Get(ctx, key)
				require.ErrorIs(t, err, storage.ErrNotFound)
			}
			_, err = store.Get(ctx, taskhub.Aria2Prefix+"legacy")
			require.NoError(t, err)
			_, err = store.Get(ctx, taskhub.Aria2Prefix+"unrelated")
			require.NoError(t, err)
			records, err := taskhub.Aria2(store).Records(ctx)
			require.NoError(t, err)
			require.Empty(t, records)
			count, err = repo.Remove(ctx, "source")
			require.NoError(t, err)
			require.Zero(t, count)
		})
	}
}

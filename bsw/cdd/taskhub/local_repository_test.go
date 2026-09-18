package taskhub_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/kv"
)

const testStoragePath = "path"

func TestLocalRepositoryConcurrentUpdatesAndDeletion(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	first, err := engine.Open("alice")
	require.NoError(t, err)
	second, err := engine.Open("alice")
	require.NoError(t, err)
	a, b := taskhub.NewLocalRepository(first), taskhub.NewLocalRepository(second)
	const id = "local-task"
	require.NoError(t, a.Save(ctx, types.LocalDownloadRecord{ID: id, Status: types.InternalDownloadStatusActive}))
	var wg sync.WaitGroup
	failures := make(chan error, 40)
	for i := range 40 {
		repo := a
		if i%2 == 0 {
			repo = b
		}
		wg.Go(func() {
			_, err := repo.Update(ctx, id, func(record *types.LocalDownloadRecord) bool { record.Completed++; return true })
			failures <- err
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	record, ok, err := a.Get(ctx, id)
	require.NoError(t, err)
	require.True(t, ok)
	require.EqualValues(t, 40, record.Completed)
	require.NoError(t, a.Remove(ctx, id))
	changed, err := b.Update(ctx, id, func(*types.LocalDownloadRecord) bool { t.Fatal("deleted task callback invoked"); return true })
	require.NoError(t, err)
	require.False(t, changed)
	records, err := a.Records(ctx)
	require.NoError(t, err)
	require.Empty(t, records)
}

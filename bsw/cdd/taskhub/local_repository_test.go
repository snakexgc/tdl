package taskhub_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
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
	require.NoError(t, a.Save(ctx, types.LocalDownloadRecord{ID: id, Status: types.LocalDownloadStatusActive}))
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

func TestLocalRepositoryRejectsIncompleteRecordsWithoutRepair(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*types.LocalDownloadRecord){
		"id":             func(r *types.LocalDownloadRecord) { r.ID = "" },
		"missing_source": func(r *types.LocalDownloadRecord) { r.TaskID = "" },
		"revision":       func(r *types.LocalDownloadRecord) { r.Revision = 0 },
		"state":          func(r *types.LocalDownloadRecord) { r.State = "" },
		"status":         func(r *types.LocalDownloadRecord) { r.Status = "" },
		"remote_status":  func(r *types.LocalDownloadRecord) { r.Status = "waiting" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			store := &storage.Memory{}
			record := types.LocalDownloadRecord{ID: "task", TaskID: "linked-file", Revision: 1, Status: types.LocalDownloadStatusQueued, State: types.DownloadQueued}
			mutate(&record)
			data, err := json.Marshal(record)
			require.NoError(t, err)
			require.NoError(t, taskhub.Local(store).Put(ctx, "task", data, time.Now()))
			repo := taskhub.NewLocalRepository(store)
			_, _, err = repo.Get(ctx, "task")
			require.Error(t, err)
			_, err = repo.Update(ctx, "task", func(*types.LocalDownloadRecord) bool { t.Fatal("invalid record reached mutation"); return true })
			require.Error(t, err)
			saved, err := taskhub.Local(store).Get(ctx, "task")
			require.NoError(t, err)
			require.Equal(t, data, saved)
		})
	}
}

package taskhub_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

const testIncompleteTask = "incomplete"

func TestDownloadStatePersistenceAndStaleObservations(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("states")
	require.NoError(t, err)
	local := taskhub.NewLocalRepository(store)
	created, err := local.Create(ctx, types.LocalDownloadRecord{ID: "local"})
	require.NoError(t, err)
	require.Equal(t, types.DownloadQueued, created.State)
	_, err = local.Update(ctx, "local", func(record *types.LocalDownloadRecord) bool { record.Status = testRemoteComplete; return true })
	require.NoError(t, err)
	_, err = local.Update(ctx, "local", func(record *types.LocalDownloadRecord) bool { record.Status = testRemoteActive; return true })
	require.ErrorContains(t, err, "state transition")
	current, _, err := local.Get(ctx, "local")
	require.NoError(t, err)
	require.Equal(t, types.DownloadComplete, current.State)
	require.EqualValues(t, 2, current.Revision)

	remote := taskhub.NewAria2Repository(store)
	require.NoError(t, remote.Add(ctx, types.Aria2TaskRecord{GID: "gid"}))
	records, err := remote.Records(ctx)
	require.NoError(t, err)
	baseline := records["gid"]
	require.Equal(t, types.DownloadQueued, baseline.State)
	finished := baseline
	finished.Status = testRemoteComplete
	finished.Total, finished.Completed = 10, 10
	applied, err := remote.Report(ctx, finished, baseline.Revision)
	require.NoError(t, err)
	require.True(t, applied)
	stale := baseline
	stale.Status = testRemoteActive
	applied, err = remote.Report(ctx, stale, baseline.Revision)
	require.NoError(t, err)
	require.False(t, applied)
	// A metadata refresh with an empty status cannot reset completion/progress.
	require.NoError(t, remote.Add(ctx, types.Aria2TaskRecord{GID: "gid", Out: "updated-name"}))
	records, err = remote.Records(ctx)
	require.NoError(t, err)
	require.Equal(t, types.DownloadComplete, records["gid"].State)
	require.EqualValues(t, 10, records["gid"].Completed)
	require.Equal(t, "updated-name", records["gid"].Out)
	require.NoError(t, remote.Remove(ctx, "gid"))
	_, err = remote.Report(ctx, finished, baseline.Revision)
	require.ErrorIs(t, err, storage.ErrNotFound)
	// Records without current concurrency metadata are rejected without writes.
	incomplete := []byte(`{"gid":"incomplete","status":"waiting","future_field":true}`)
	require.NoError(t, taskhub.Aria2(store).Put(ctx, testIncompleteTask, incomplete, time.Now()))
	applied, err = remote.Report(ctx, types.Aria2TaskRecord{GID: testIncompleteTask, Status: "paused"}, 0)
	require.ErrorContains(t, err, "invalid aria2 task record")
	require.False(t, applied)
	require.Error(t, remote.Add(ctx, types.Aria2TaskRecord{GID: testIncompleteTask}))
	_, reserved, err := remote.ReserveControl(ctx, types.Aria2TaskRecord{GID: testIncompleteTask}, time.Now().Add(time.Minute))
	require.Error(t, err)
	require.False(t, reserved)
	_, err = remote.Records(ctx)
	require.Error(t, err)
	raw, err := taskhub.Aria2(store).Get(ctx, testIncompleteTask)
	require.NoError(t, err)
	require.Equal(t, incomplete, raw)
}

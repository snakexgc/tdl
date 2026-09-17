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
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestOwnedReportsDoNotLoseConcurrentState(t *testing.T) {
	ctx := context.Background()
	engine, err := kv.New(kv.DriverBolt, map[string]any{"path": filepath.Join(t.TempDir(), "db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	first, err := engine.Open("account")
	require.NoError(t, err)
	second, err := engine.Open("account")
	require.NoError(t, err)
	now := time.Now().UTC()
	stale := now.Add(-2 * time.Hour)
	metadata, err := json.Marshal(map[string]any{"id": "link", "created_at": stale, "last_active_at": stale, "completed": 0})
	require.NoError(t, err)
	require.NoError(t, taskhub.Links(first).Put(ctx, "link", metadata, stale))
	var wg sync.WaitGroup
	errors := make(chan error, 21)
	for range 20 {
		wg.Go(func() {
			errors <- taskhub.Links(first).Mutate(ctx, "link", func(data []byte, _ time.Time) ([]byte, time.Time, error) {
				var raw map[string]json.RawMessage
				if err := json.Unmarshal(data, &raw); err != nil {
					return nil, now, err
				}
				var completed int
				if err := json.Unmarshal(raw["completed"], &completed); err != nil {
					return nil, now, err
				}
				raw["completed"], _ = json.Marshal(completed + 1)
				raw["last_active_at"], _ = json.Marshal(now)
				next, err := json.Marshal(raw)
				return next, now, err
			})
		})
	}
	wg.Go(func() { errors <- taskhub.Links(second).MarkDownloaded(ctx, "link") })
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	// Delayed metadata must not roll back the newer expiry clock or status.
	patch, err := json.Marshal(map[string]any{"file_name": "fresh", "last_active_at": stale})
	require.NoError(t, err)
	require.NoError(t, taskhub.Links(second).Merge(ctx, "link", patch, stale))
	require.NoError(t, taskhub.CleanupLinksAndAria2(ctx, first, now, time.Hour))
	data, err := taskhub.Links(second).Get(ctx, "link")
	require.NoError(t, err)
	var result struct {
		Completed    int
		Downloaded   bool
		LastActiveAt time.Time `json:"last_active_at"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, 20, result.Completed)
	require.True(t, result.Downloaded)
	require.True(t, now.Equal(result.LastActiveAt))
	deleted, err := taskhub.DeleteSnapshotKey(ctx, first, taskhub.LinkPrefix+"link", metadata)
	require.NoError(t, err)
	require.False(t, deleted, "maintenance must preserve records changed after its snapshot")
	require.NoError(t, taskhub.Links(first).Remove(ctx, "link"))
	require.ErrorIs(t, taskhub.Links(second).MarkDownloaded(ctx, "link"), storage.ErrNotFound)
}

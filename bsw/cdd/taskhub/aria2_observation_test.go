package taskhub_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestObservationCannotResurrectDeletedOrOverwriteControlledTask(t *testing.T) {
	for _, driver := range []kv.Driver{kv.DriverBolt, kv.DriverLegacy, kv.DriverFile} {
		t.Run(string(driver), func(t *testing.T) {
			ctx := context.Background()
			now := time.Now()
			engine, err := kv.New(driver, map[string]any{testStoragePath: filepath.Join(t.TempDir(), "observations")})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, engine.Close()) })
			store, err := engine.Open("observations")
			require.NoError(t, err)
			links := taskhub.LinkRepository{Store: store, Engine: engine, Namespace: "observations"}
			repo := taskhub.Aria2Observations{Links: links}
			require.NoError(t, taskhub.Links(store).Put(ctx, "source", []byte(`{"id":"source","media":{"secret":"preserved"}}`), now))
			snapshot, err := repo.Snapshot(ctx)
			require.NoError(t, err)
			observation := types.Aria2TaskRecord{GID: "remote-retry", TaskID: "source", Status: testRemoteActive, CreatedAt: now}
			applied, err := repo.Apply(ctx, snapshot.Links["source"], observation, true, now, time.Hour)
			require.NoError(t, err)
			require.True(t, applied)
			snapshot, err = repo.Snapshot(ctx)
			require.NoError(t, err)
			observation = snapshot.Records[observation.GID]
			// A pause changes the revision after the poll's repository snapshot.
			paused := observation
			paused.Status = "paused"
			require.NoError(t, taskhub.NewAria2Repository(store, 0).Add(ctx, paused))
			observation.Status = testRemoteComplete
			applied, err = repo.Apply(ctx, snapshot.Links["source"], observation, false, now, time.Hour)
			require.NoError(t, err)
			require.False(t, applied)
			data, err := store.Get(ctx, taskhub.LinkPrefix+"source")
			require.NoError(t, err)
			var link map[string]any
			require.NoError(t, json.Unmarshal(data, &link))
			require.NotEqual(t, true, link["downloaded"])
			require.Contains(t, link, "media")
			_, err = links.Remove(ctx, "source")
			require.NoError(t, err)
			applied, err = repo.Apply(ctx, snapshot.Links["source"], observation, true, now, time.Hour)
			require.NoError(t, err)
			require.False(t, applied)
			_, err = store.Get(ctx, taskhub.Aria2Prefix+observation.GID)
			require.ErrorIs(t, err, storage.ErrNotFound)
		})
	}
}

const testRemoteActive = "active"

const testRemoteComplete = "complete"

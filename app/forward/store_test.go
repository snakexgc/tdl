package forward

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	"github.com/snakexgc/tdl/internal/core/storage"
)

func TestForwardStoresShareAtomicIndex(t *testing.T) {
	ctx := context.Background()
	kv := newMemStorage()
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Go(func() {
			errs <- newJobStore(kv).Save(ctx, Job{ID: fmt.Sprint(i), Status: StatusPaused})
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	records, err := newJobStore(kv).Records(ctx)
	require.NoError(t, err)
	require.Len(t, records, 40)
	require.NoError(t, kv.Set(ctx, taskhub.ForwardIndex, []byte("broken")))
	require.Error(t, newJobStore(kv).Save(ctx, Job{ID: "orphan"}))
	_, err = kv.Get(ctx, taskhub.ForwardPrefix+"orphan")
	require.ErrorIs(t, err, storage.ErrNotFound)
}

func TestForwardLegacyRecordsAndMaintenance(t *testing.T) {
	ctx := context.Background()
	kv := newMemStorage()
	repo := taskhub.Forward(kv)
	data := []byte(`{"source_link":"https://t.me/c/1/2"}`)
	require.NoError(t, repo.Put(ctx, "legacy", data, time.Now()))
	job, ok, err := newJobStore(kv).Get(ctx, "legacy")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "legacy", job.ID)
	require.Equal(t, StatusQueued, job.Status)
	deleted, err := taskhub.DeleteSnapshotKey(ctx, kv, taskhub.ForwardPrefix+"legacy", data)
	require.NoError(t, err)
	require.True(t, deleted)
	index, err := kv.Get(ctx, taskhub.ForwardIndex)
	require.NoError(t, err)
	require.JSONEq(t, `{}`, string(index))
}

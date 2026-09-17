package storage_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gotd/td/telegram/updates"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestStateConcurrentUpdatesAndChannelPreservation(t *testing.T) {
	engine, err := kv.New(kv.DriverBolt, map[string]any{"path": filepath.Join(t.TempDir(), "state")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	kvd, err := engine.Open("default")
	require.NoError(t, err)
	s := storage.NewState(kvd)
	ctx := context.Background()
	require.NoError(t, s.SetState(ctx, 1, updates.State{}))
	var wg sync.WaitGroup
	for i := int64(1); i <= 64; i++ {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if err := s.SetChannelPts(ctx, 1, id, int(id)); err != nil {
				t.Error(err)
			}
		}(i)
	}
	for _, update := range []func() error{
		func() error { return s.SetPts(ctx, 1, 11) }, func() error { return s.SetQts(ctx, 1, 22) }, func() error { return s.SetDateSeq(ctx, 1, 33, 44) },
	} {
		wg.Add(1)
		go func(fn func() error) {
			defer wg.Done()
			if err := fn(); err != nil {
				t.Error(err)
			}
		}(update)
	}
	wg.Wait()
	state, ok, err := s.GetState(ctx, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, updates.State{Pts: 11, Qts: 22, Date: 33, Seq: 44}, state)
	require.NoError(t, s.SetState(ctx, 1, state))
	count := 0
	require.NoError(t, s.ForEachChannels(ctx, 1, func(_ context.Context, id int64, pts int) error { count++; require.Equal(t, int(id), pts); return nil }))
	require.Equal(t, 64, count)
}

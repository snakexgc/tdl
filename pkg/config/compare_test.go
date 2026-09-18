package config

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func TestCompareAndSetRejectsStaleConfiguration(t *testing.T) {
	mu.Lock()
	previous, previousPath := instance, configPath
	instance, configPath = DefaultConfig(), filepath.Join(t.TempDir(), "config.json")
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); instance, configPath = previous, previousPath; mu.Unlock() })
	before, err := Clone(Get())
	require.NoError(t, err)
	concurrent, err := Clone(before)
	require.NoError(t, err)
	concurrent.Limit = 2
	require.NoError(t, Set(concurrent))
	stale, err := Clone(before)
	require.NoError(t, err)
	stale.Limit = 3
	require.ErrorIs(t, CompareAndSet(context.Background(), before, stale), ports.ErrConfigurationConflict)
	require.Equal(t, 2, Get().Limit)
	current, err := Clone(Get())
	require.NoError(t, err)
	require.NoError(t, CompareAndSet(context.Background(), current, stale))
	require.Equal(t, 3, Get().Limit)
	persisted, err := Load(configPath)
	require.NoError(t, err)
	require.Equal(t, 3, persisted.Limit)
}

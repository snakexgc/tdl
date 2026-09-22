package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func TestCompareAndSetRejectsStaleConfiguration(t *testing.T) {
	store := installTestConfiguration(t)
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
	persisted := store.value
	require.Equal(t, 3, persisted.Limit)
}

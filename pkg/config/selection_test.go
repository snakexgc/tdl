package config

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func TestNamespaceSelectionPreservesNewSettingsAndRejectsStaleLogin(t *testing.T) {
	store := installTestConfiguration(t)
	ctx := context.Background()
	startingAccount := Get().Namespace
	updated, err := Clone(Get())
	require.NoError(t, err)
	updated.Limit, updated.Delay = 13, 27
	require.NoError(t, Set(updated))
	changed, err := SelectNamespace(ctx, startingAccount, "alice")
	require.NoError(t, err)
	require.True(t, changed)
	persisted := store.value
	require.Equal(t, "alice", persisted.Namespace)
	require.Equal(t, 13, persisted.Limit)
	require.Equal(t, 27, persisted.Delay)
	changed, err = SelectNamespace(ctx, startingAccount, "bob")
	require.ErrorIs(t, err, ports.ErrConfigurationConflict)
	require.False(t, changed)
	require.Equal(t, "alice", Get().Namespace)
	changed, err = SelectNamespace(ctx, "alice", "alice")
	require.NoError(t, err)
	require.False(t, changed)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = SelectNamespace(canceled, "alice", "bob")
	require.ErrorIs(t, err, context.Canceled)
	_, err = SelectNamespace(ctx, "alice", "../outside")
	require.Error(t, err)
	store.failure = errors.New("storage unavailable")
	_, err = SelectNamespace(ctx, "alice", "bob")
	require.Error(t, err)
	require.Equal(t, "alice", Get().Namespace, "failed persistence must not publish selection")
}

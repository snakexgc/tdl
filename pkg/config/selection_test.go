package config

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
)

func TestNamespaceSelectionPreservesNewSettingsAndRejectsStaleLogin(t *testing.T) {
	mu.Lock()
	previous, previousPath := instance, configPath
	instance, configPath = DefaultConfig(), filepath.Join(t.TempDir(), "config.json")
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); instance, configPath = previous, previousPath; mu.Unlock() })
	ctx := context.Background()
	startingAccount := Get().Namespace
	updated, err := Clone(Get())
	require.NoError(t, err)
	updated.Limit, updated.Delay = 13, 27
	require.NoError(t, Set(updated))
	changed, err := SelectNamespace(ctx, startingAccount, "alice")
	require.NoError(t, err)
	require.True(t, changed)
	persisted, err := Load(configPath)
	require.NoError(t, err)
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
	mu.Lock()
	configPath = filepath.Join(t.TempDir(), "missing", "config.json")
	mu.Unlock()
	_, err = SelectNamespace(ctx, "alice", "bob")
	require.Error(t, err)
	require.Equal(t, "alice", Get().Namespace, "failed persistence must not publish selection")
}

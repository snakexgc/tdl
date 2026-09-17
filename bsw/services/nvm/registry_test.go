package nvm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/internal/core/storage"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestDatasetOwnershipAndScope(t *testing.T) {
	engine, err := kv.New(kv.DriverBolt, map[string]any{"path": filepath.Join(t.TempDir(), "db")})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	store, err := engine.Open("account-a")
	require.NoError(t, err)
	registry := New(store)
	dataset := Dataset{Name: "tasks", Writer: "taskhub", Prefix: "tasks."}
	require.NoError(t, registry.Register(dataset))
	require.Error(t, registry.Register(dataset))
	require.Error(t, registry.Register(Dataset{Name: "overlap", Writer: "other", Prefix: "tasks.child."}))
	_, err = registry.Writer("tasks", "panel")
	require.Error(t, err)
	readOnly, err := registry.Reader("tasks")
	require.NoError(t, err)
	_, writable := readOnly.(storage.Storage)
	require.False(t, writable, "readers must not expose a hidden write capability")
	writer, err := registry.Writer("tasks", "taskhub")
	require.NoError(t, err)
	ctx := context.Background()
	require.NoError(t, writer.Set(ctx, "tasks.1", []byte("before")))
	require.Error(t, writer.Set(ctx, "session", []byte("escape")))
	require.Error(t, writer.Delete(ctx, "session"))
	_, err = readOnly.Get(ctx, "session")
	require.Error(t, err)
	err = storage.Update(ctx, writer, func(tx storage.Storage) error {
		if err := tx.Set(ctx, "tasks.1", []byte("after")); err != nil {
			return err
		}
		return tx.Set(ctx, "outside", nil)
	})
	require.Error(t, err)
	data, err := readOnly.Get(ctx, "tasks.1")
	require.NoError(t, err)
	require.Equal(t, "before", string(data), "scope violations roll back the entire transaction")
}

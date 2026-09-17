package config_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
)

const secretField = "token"

func TestSecretGenerationPublication(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := config.NewStore(dir)
	schema := []manifest.ConfigField{{Name: secretField, Type: manifest.String, Default: "", Secret: true}}
	view, err := config.New(schema, map[string]any{secretField: "private-first"})
	require.NoError(t, err)
	require.NoError(t, store.Save(ctx, "example", true, view))
	path := filepath.Join(dir, "swc-example.json")
	public, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(public), "private-first")
	var before config.Document
	require.NoError(t, json.Unmarshal(public, &before))
	require.NotEmpty(t, before.Secrets)
	loaded, err := store.Load(ctx, "example")
	require.NoError(t, err)
	require.Equal(t, "private-first", loaded.Values[secretField])
	view, err = config.New(schema, map[string]any{secretField: "private-second"})
	require.NoError(t, err)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, store.Save(canceled, "example", true, view), context.Canceled)
	unchanged, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, public, unchanged)
	require.NoError(t, store.Save(ctx, "example", true, view))
	loaded, err = store.Load(ctx, "example")
	require.NoError(t, err)
	require.Equal(t, "private-second", loaded.Values[secretField])
	require.NotEqual(t, before.Secrets, loaded.Secrets)
	// Readers of the old document can still finish loading its generation.
	old, err := os.ReadFile(filepath.Join(dir, "secrets", before.Secrets))
	require.NoError(t, err)
	require.Contains(t, string(old), "private-first")
	before.Secrets = "../outside.json"
	bad, err := json.Marshal(before)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bad, 0o600))
	_, err = store.Load(ctx, "example")
	require.ErrorContains(t, err, "invalid secret reference")
}

func TestFailedPublicCommitRemovesUnpublishedSecrets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swc-example.json")
	require.NoError(t, os.Mkdir(path, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(path, "occupied"), nil, 0o600))
	view, err := config.New([]manifest.ConfigField{{Name: secretField, Type: manifest.String, Secret: true}}, map[string]any{secretField: "unpublished"})
	require.NoError(t, err)
	require.Error(t, config.NewStore(dir).Save(context.Background(), "example", true, view))
	files, err := os.ReadDir(filepath.Join(dir, "secrets"))
	require.NoError(t, err)
	require.Empty(t, files)
	_, err = os.Stat(filepath.Join(path, "occupied"))
	require.NoError(t, err)
}

package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/manifest"
	"github.com/snakexgc/tdl/rte/config"
	"github.com/snakexgc/tdl/rte/configtest"
)

func TestStartupSnapshotIsIsolatedFromSavedAndReturnedValues(t *testing.T) {
	ctx := context.Background()
	saved := configtest.NewStore()
	view, err := config.New([]manifest.ConfigField{{Name: "nested", Type: manifest.Objects, Default: []map[string]any{{"value": "original"}}}}, nil)
	require.NoError(t, err)
	require.NoError(t, saved.Save(ctx, "example", true, view))
	active, err := saved.Snapshot(ctx, []string{"example"})
	require.NoError(t, err)
	revision, err := active.Revision(ctx, "example")
	require.NoError(t, err)
	require.NoError(t, saved.Save(ctx, "example", false, config.View{}))
	first, err := active.Load(ctx, "example")
	require.NoError(t, err)
	first.Values["nested"].([]any)[0].(map[string]any)["value"] = "mutated"
	again, err := active.Load(ctx, "example")
	require.NoError(t, err)
	require.True(t, again.Enabled)
	require.Equal(t, "original", again.Values["nested"].([]any)[0].(map[string]any)["value"])
	current, err := active.Revision(ctx, "example")
	require.NoError(t, err)
	require.Equal(t, revision, current)
	require.ErrorContains(t, active.Save(ctx, "example", true, config.View{}), "read-only")
	_, err = active.Load(ctx, "missing")
	require.Error(t, err)
}

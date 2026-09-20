package migration_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/internal/migration"
	"github.com/snakexgc/tdl/rte"
	"github.com/snakexgc/tdl/rte/config"
)

const legacyInput = `{"namespace":"migrationtest","filename":"custom-F","telegram":{"api_id":12345,"api_hash":"secret-application-value"},"downloader":{"mode":"internal"}}`

func TestMigrationPreviewExportAndRebuild(t *testing.T) {
	ctx := context.Background()
	plan, err := migration.Prepare(strings.NewReader(legacyInput))
	require.NoError(t, err)
	require.NoError(t, plan.Validate(ctx))
	preview, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NotContains(t, string(preview), "secret-application-value")
	require.Len(t, plan.Components, 18)
	target := filepath.Join(t.TempDir(), "components")
	require.NoError(t, plan.Write(ctx, target))
	public, err := os.ReadFile(filepath.Join(target, "swc-account.telegram.json"))
	require.NoError(t, err)
	require.NotContains(t, string(public), "secret-application-value")
	store := config.NewStore(target)
	doc, err := store.Load(ctx, "account.telegram")
	require.NoError(t, err)
	require.Equal(t, "secret-application-value", doc.Values["api_hash"])
	require.Equal(t, false, doc.Values["use_builtin"], "migration must preserve custom credentials")
	require.Equal(t, "", doc.Values["builtin_preset"])
	registry, err := application.Registry()
	require.NoError(t, err)
	host, err := registry.BuildStored(ctx, plan.Account, store)
	require.NoError(t, err)
	for _, status := range host.Start(ctx) {
		require.Equal(t, rte.Running, status.State, status.Detail)
	}
	require.NoError(t, host.Stop(ctx))
	require.Error(t, plan.Write(ctx, target))
	unchanged, err := os.ReadFile(filepath.Join(target, "swc-account.telegram.json"))
	require.NoError(t, err)
	require.Equal(t, public, unchanged)
}

func TestMigrationRejectsInvalidInputWithoutOutput(t *testing.T) {
	_, err := migration.Prepare(strings.NewReader(`{"unknown_option":true}`))
	require.Error(t, err)
	_, err = migration.Prepare(strings.NewReader(`{} {}`))
	require.Error(t, err)
	plan, err := migration.Prepare(strings.NewReader(`{"filename":"{{"}`))
	require.NoError(t, err)
	target := filepath.Join(t.TempDir(), "invalid")
	require.Error(t, plan.Write(context.Background(), target))
	_, err = os.Stat(target)
	require.True(t, os.IsNotExist(err))
	plan, err = migration.Prepare(strings.NewReader(legacyInput))
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, plan.Write(ctx, target))
	_, err = os.Stat(target)
	require.True(t, os.IsNotExist(err))
}

func TestLegacyMinimumAndSizeLimit(t *testing.T) {
	plan, err := migration.Prepare(strings.NewReader(`{"file_size_mb":17}`))
	require.NoError(t, err)
	target := filepath.Join(t.TempDir(), "legacy")
	require.NoError(t, plan.Write(context.Background(), target))
	doc, err := config.NewStore(target).Load(context.Background(), "filter.rules")
	require.NoError(t, err)
	require.Equal(t, json.Number("17"), doc.Values["min_mb"])
	_, err = migration.Prepare(strings.NewReader(`{}` + strings.Repeat(" ", 1<<20)))
	require.ErrorContains(t, err, "size limit")
}

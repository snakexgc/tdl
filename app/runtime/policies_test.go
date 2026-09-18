package runtime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/app/watch"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/internal/migration"
	"github.com/snakexgc/tdl/pkg/config"
	rteconfig "github.com/snakexgc/tdl/rte/config"
)

func TestProductionPolicyHostPreservesLastValidConfiguration(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHost(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{policies: host, filter: filter, naming: naming}
	require.NoError(t, manager.initDirectory())
	opts := manager.watchOptions(cfg)
	require.Same(t, filter, opts.Filter)
	require.Same(t, naming, opts.Naming)
	input := ports.NamingInput{BaseDir: "/downloads", Data: ports.NamingData{FileName: "video.mp4"}}
	before, err := naming.Render(ctx, input)
	require.NoError(t, err)
	invalid := opts
	invalid.Template = "{{"
	require.Error(t, host.ReconfigureBatch(ctx, watch.PolicyValues(invalid)))
	after, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Equal(t, before, after)
	valid := opts
	valid.Template = "new-F"
	require.NoError(t, host.ReconfigureBatch(ctx, watch.PolicyValues(valid)))
	after, err = opts.Naming.Render(ctx, input)
	require.NoError(t, err)
	require.NotEqual(t, before, after)
}

func TestProductionStoredPoliciesSurviveRestart(t *testing.T) {
	ctx := context.Background()
	plan, err := migration.Prepare(strings.NewReader(`{"filename":"imported-F"}`))
	require.NoError(t, err)
	directory := filepath.Join(t.TempDir(), "components")
	require.NoError(t, plan.Write(ctx, directory))
	store := rteconfig.NewStore(directory)
	cfg := config.DefaultConfig()
	host, filter, naming, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, host.Stop(ctx)) })
	manager := &Manager{policies: host, filter: filter, naming: naming, componentStore: store}
	require.NoError(t, manager.initDirectory())
	input := ports.NamingInput{BaseDir: "/downloads", Data: ports.NamingData{FileName: "sample.mp4"}}
	before, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Contains(t, before.FileName, "imported-")
	require.NoError(t, manager.SaveComponentConfiguration(ctx, "naming.rules", map[string]any{"filename": "saved-F"}))
	after, err := naming.Render(ctx, input)
	require.NoError(t, err)
	require.Contains(t, after.FileName, "saved-")
	require.Error(t, manager.SaveComponentConfiguration(ctx, "naming.rules", map[string]any{"filename": "{{"}))
	require.NoError(t, host.Stop(ctx))
	restarted, _, reloaded, err := newPolicyHostStored(ctx, cfg, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, restarted.Stop(ctx)) })
	result, err := reloaded.Render(ctx, input)
	require.NoError(t, err)
	require.Equal(t, after, result)
}

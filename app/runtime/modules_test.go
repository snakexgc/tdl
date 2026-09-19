package runtime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestLegacyModuleTogglePublishesSavedConfiguration(t *testing.T) {
	// Init is process-wide. Keep the compatibility store and its once guard
	// isolated from other runtime tests, including repeated test runs.
	const child = "TDL_TEST_LEGACY_MODULE_TOGGLE"
	if os.Getenv(child) != "1" {
		cmd := exec.CommandContext(context.Background(), os.Args[0], "-test.run=^TestLegacyModuleTogglePublishesSavedConfiguration$")
		cmd.Env = append(os.Environ(), child+"=1")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
		return
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	initial := config.DefaultConfig()
	initial.Modules = config.ModulesConfig{WebUI: true}
	require.NoError(t, config.Save(path, initial))
	require.NoError(t, config.Init(directory))
	ctx, cancel := context.WithCancel(context.Background())
	manager := NewManager(config.WithSource(ctx, config.NewSource(config.Get())), nil, nil, Options{})
	t.Cleanup(manager.Shutdown)
	// No backend is started by this test: inspect persistence and publication
	// while background reconciliation sees an already canceled host context.
	cancel()

	state, err := manager.SetModuleEnabled(context.Background(), "webui", false)
	require.NoError(t, err)
	manager.transitionWG.Wait()
	require.False(t, state.Enabled)
	require.False(t, config.From(manager.parent).Modules.WebUI)
	persisted, err := config.Load(path)
	require.NoError(t, err)
	require.False(t, persisted.Modules.WebUI)

	concurrent, err := config.Clone(config.Get())
	require.NoError(t, err)
	concurrent.Delay++
	require.NoError(t, config.Set(concurrent))
	_, err = manager.SetModuleEnabled(context.Background(), "webui", true)
	require.ErrorIs(t, err, ports.ErrConfigurationConflict)
	require.False(t, config.From(manager.parent).Modules.WebUI)
	require.Equal(t, concurrent.Delay, config.Get().Delay)
	require.False(t, config.Get().Modules.WebUI)

	manager.configSource.Replace(concurrent)
	canceled, cancelRequest := context.WithCancel(context.Background())
	cancelRequest()
	_, err = manager.SetModuleEnabled(canceled, "webui", true)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, config.From(manager.parent).Modules.WebUI)
	require.False(t, config.Get().Modules.WebUI)

	require.NoError(t, os.Rename(path, path+".saved"))
	require.NoError(t, os.Mkdir(path, 0o700))
	_, err = manager.SetModuleEnabled(context.Background(), "webui", true)
	require.Error(t, err)
	require.False(t, config.From(manager.parent).Modules.WebUI)
	require.False(t, config.Get().Modules.WebUI)
}

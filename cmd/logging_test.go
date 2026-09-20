package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/pkg/consts"
	"github.com/snakexgc/tdl/pkg/kv"
)

func TestCommandFailureClosesLogs(t *testing.T) {
	const helperEnv = "TDL_TEST_LOG_FAILURE"
	if phase := os.Getenv(helperEnv); phase != "" {
		previous := slog.Default()
		command := New()
		command.SetContext(context.Background())
		switch phase {
		case "initialization":
			DefaultBoltStorage = map[string]string{kv.DriverTypeKey: "invalid-test-driver"}
			command.SetArgs(nil)
			require.Error(t, command.Execute())
		case "run":
			require.NoError(t, command.PersistentPreRunE(command, nil))
			// Removing the required flag makes RunE fail before starting services.
			command.ResetFlags()
			require.Error(t, command.RunE(command, nil))
			store, err := kv.NewWithMap(DefaultBoltStorage)
			require.NoError(t, err, "the previous database handle must be closed")
			require.NoError(t, store.Close())
		}
		require.Same(t, previous, slog.Default())
		for _, name := range []string{"latest.log", "events.jsonl"} {
			path := filepath.Join(consts.LogPath, name)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Contains(t, string(data), "失败")
			require.NoError(t, os.Rename(path, path+".closed"), "log handle must be released")
		}
		return
	}
	for _, phase := range []string{"initialization", "run"} {
		t.Run(phase, func(t *testing.T) {
			executable, err := os.Executable()
			require.NoError(t, err)
			child := exec.Command(executable, "-test.run=^TestCommandFailureClosesLogs$")
			child.Env = append(os.Environ(), helperEnv+"="+phase, consts.EnvHome+"="+t.TempDir())
			output, err := child.CombinedOutput()
			require.NoError(t, err, string(output))
		})
	}
}

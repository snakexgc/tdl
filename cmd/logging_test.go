package cmd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
			openStorage = func() (kv.Storage, error) { return nil, errors.New("test storage initialization failure") }
			command.SetArgs(nil)
			require.Error(t, command.Execute())
		case "run":
			require.NoError(t, command.PersistentPreRunE(command, nil))
			// Removing startup state makes RunE fail before starting services.
			command.SetContext(context.WithValue(command.Context(), startupConfigurationKey{}, false))
			require.Error(t, command.RunE(command, nil))
			store, err := kv.New(kv.DriverBolt, consts.DataDir)
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
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestCommandFailureClosesLogs$")
			child.Env = append(os.Environ(), helperEnv+"="+phase, consts.EnvHome+"="+t.TempDir())
			output, err := child.CombinedOutput()
			require.NoError(t, err, string(output))
			require.NotContains(t, string(output), "TDL 正在启动")
			require.NotContains(t, string(output), "TDL 初始化失败")
			require.NotContains(t, string(output), "TDL 运行失败")
		})
	}
}

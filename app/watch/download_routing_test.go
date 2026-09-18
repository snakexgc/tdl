package watch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

type fixedDownloadRoute struct{ route ports.DownloadRoute }

func (f fixedDownloadRoute) Route(context.Context, types.AccountID) (ports.DownloadRoute, error) {
	return f.route, nil
}

func TestRoutedFallbackUsesIndependentLocalPathOnlyAfterDefiniteRejection(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			ctx := context.Background()
			w := namingWatcher(t, "F", 255)
			w.opts.Account = types.DefaultAccount
			root := filepath.Join(t.TempDir(), localExecutorName)
			route := ports.DownloadRoute{Executors: []string{"aria2", localExecutorName}, LocalRoot: root}
			w.opts.DownloadRouting = fixedDownloadRoute{route}
			cfg := config.DefaultConfig()
			cfg.HTTP.PublicBaseURL = "http://localhost:8090"
			w.runtime = newWatchRuntime(cfg, w.opts, newMemoryTaskStorage(), nil)
			w.runtime.outputRoot = filepath.Join(t.TempDir(), "remote-only")
			remoteCalls, localCalls := 0, 0
			w.opts.DownloadSubmitter = preparedExecutor{name: "aria2", submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				remoteCalls++
				require.NotEqual(t, root, in.Dir)
				if ambiguous {
					return types.DownloadResult{}, errors.New("reply lost")
				}
				return types.DownloadResult{}, ports.ErrDownloadNotAccepted
			}}
			w.runtime.local = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				localCalls++
				require.Equal(t, root, in.Dir)
				require.Equal(t, filepath.Join(root, testVideoFile), in.FullPath)
				return types.DownloadResult{Target: localExecutorName, ID: in.TaskID}, nil
			}}
			file := fileTask{peer: &tg.InputPeerUser{UserID: 1}, msg: &tg.Message{ID: 2}, media: &tmedia.Media{Name: testVideoFile, Size: 4}}
			prepared, skip, err := w.prepareSingle(ctx, file)
			require.NoError(t, err)
			require.False(t, skip)
			require.NoDirExists(t, root)
			err = w.submitRouted(ctx, prepared, "task", route)
			require.Equal(t, 1, remoteCalls)
			if ambiguous {
				require.Error(t, err)
				require.Zero(t, localCalls)
				require.NoDirExists(t, root)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, localCalls)
				require.DirExists(t, root)
			}
			require.NoDirExists(t, w.runtime.outputRoot)
		})
	}
}

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

func TestModeSwitchDoesNotReroutePreparedLocalTask(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeLocal
	cfg.Downloader.LocalRoot = t.TempDir()
	cfg.Aria2.Dir = filepath.Join(t.TempDir(), "remote-only")
	source := config.NewSource(cfg)
	ctx := config.WithSource(context.Background(), source)
	w := namingWatcher(t, "F", 255)
	w.opts.Account = types.DefaultAccount
	w.opts.DownloadRouting = fixedDownloadRoute{ports.DownloadRoute{Mode: config.DownloaderModeLocal, LocalRoot: cfg.Downloader.LocalRoot}}
	w.runtime = newWatchRuntime(cfg, w.opts, newMemoryTaskStorage(), nil)
	called := false
	w.runtime.local = preparedExecutor{name: localExecutorName, submit: func(_ context.Context, request types.DownloadSubmission) (types.DownloadResult, error) {
		called = true
		require.Equal(t, cfg.Downloader.LocalRoot, request.Dir)
		return types.DownloadResult{Target: localExecutorName}, nil
	}}
	w.opts.DownloadSubmitter = preparedExecutor{name: config.DownloaderModeAria2, submit: func(context.Context, types.DownloadSubmission) (types.DownloadResult, error) {
		t.Error("prepared local task was sent remotely")
		return types.DownloadResult{}, nil
	}}
	file := fileTask{peerID: 1, peer: &tg.InputPeerUser{UserID: 1}, msg: &tg.Message{ID: 2}, media: &tmedia.Media{Name: testVideoFile, Size: 4, InputFileLoc: &tg.InputDocumentFileLocation{ID: 99}}}
	prepared, skip, err := w.prepareSingle(ctx, file)
	require.NoError(t, err)
	require.False(t, skip)
	next, err := config.Clone(cfg)
	require.NoError(t, err)
	next.Downloader.Mode = config.DownloaderModeAria2
	source.Replace(next)
	require.NoError(t, w.submitSingle(ctx, prepared))
	require.True(t, called)
	require.NoDirExists(t, cfg.Aria2.Dir)
}

func TestRoutedFallbackUsesIndependentLocalPathOnlyAfterDefiniteRejection(t *testing.T) {
	for _, ambiguous := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "ambiguous"}[ambiguous], func(t *testing.T) {
			ctx := context.Background()
			w := namingWatcher(t, "F", 255)
			w.opts.Account = types.DefaultAccount
			root := filepath.Join(t.TempDir(), localExecutorName)
			route := ports.DownloadRoute{Executors: []string{config.DownloaderModeAria2, localExecutorName}, LocalRoot: root}
			w.opts.DownloadRouting = fixedDownloadRoute{route}
			cfg := config.DefaultConfig()
			cfg.HTTP.PublicBaseURL = "http://localhost:8090"
			w.runtime = newWatchRuntime(cfg, w.opts, newMemoryTaskStorage(), nil)
			w.runtime.outputRoot = filepath.Join(t.TempDir(), "remote-only")
			remoteCalls, localCalls := 0, 0
			w.opts.DownloadSubmitter = preparedExecutor{name: config.DownloaderModeAria2, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
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

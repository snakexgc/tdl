package watch

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gotd/td/fileid"
	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/application"
	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestForwardedBotFileUsesConfiguredDownloadPipeline(t *testing.T) {
	fileID, err := fileid.EncodeFileID(fileid.FileID{
		Type: fileid.Document, DC: 2, ID: 78, AccessHash: 90, FileReference: []byte("bot-reference"),
	})
	require.NoError(t, err)
	for _, executor := range []string{localExecutorName, config.DownloadExecutorAria2} {
		t.Run(executor, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.HTTP.PublicBaseURL = "http://127.0.0.1:8080"
			ctx := config.WithSource(context.Background(), config.NewSource(cfg))
			w := namingWatcher(t, "F", 255)
			w.opts.Account, w.opts.Download = types.DefaultAccount, true
			w.opts.DownloadRouting = &fixedDownloadRoute{ports.DownloadRoute{Executors: []string{executor}, LocalRoot: t.TempDir()}}
			w.runtime = newTestWatchRuntime(cfg, w.opts, newMemoryTaskStorage(), nil)
			accepted := false
			backend := preparedExecutor{name: executor, submit: func(_ context.Context, in types.DownloadSubmission) (types.DownloadResult, error) {
				accepted = true
				require.Equal(t, "document_78", in.TaskID)
				if executor == config.DownloadExecutorAria2 {
					require.Contains(t, in.DownloadURL, "/download/document_78")
				}
				return types.DownloadResult{Account: in.Account, Target: executor, ID: in.TaskID}, nil
			}}
			if executor == localExecutorName {
				w.runtime.local = backend
			} else {
				cfg.Aria2.Dir = filepath.Join(t.TempDir(), "remote")
				w.opts.DownloadSubmitter = backend
			}
			host, pipeline, err := application.DownloadControlHost(ctx, types.DefaultAccount, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, host.Stop(context.Background())) })
			w.opts.DownloadPipeline = pipeline.(ports.DownloadPipeline)
			request := types.DownloadIntent{
				Account: types.DefaultAccount, PeerID: 42, MessageID: 17,
				Link: "转发消息 17", Source: types.DownloadSourceBotForward,
				Forwarded: &types.ForwardedMedia{FileID: fileID, FileName: "报告.pdf", FileSize: 123, Date: 1700000000, PeerName: "来源"},
			}
			result, err := w.processDownloadIntent(ctx, request)
			require.NoError(t, err)
			require.True(t, accepted)
			require.Equal(t, 1, result.Total)
			require.Equal(t, 1, result.Queued)
			task, ok, err := w.runtime.proxy.Tasks().Get(ctx, "document_78")
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, fileID, task.BotFileID)
			require.Nil(t, task.Peer)
			require.Equal(t, "报告.pdf", task.Media.Name)
		})
	}
}

package watch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

func TestLocalExecutorUsesAccountTaskRepository(t *testing.T) {
	ctx := context.Background()
	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeLocal
	opts := DefaultOptions(cfg)
	opts.Account = "alice"
	runtime := newWatchRuntime(cfg, opts, newMemoryTaskStorage(), nil)
	in := types.DownloadSubmission{Account: "bob", TaskID: testDocument1, FullPath: filepath.Join(t.TempDir(), testVideoFile)}
	_, err := runtime.local.Submit(ctx, in)
	require.ErrorContains(t, err, "account mismatch")
	in.Account = opts.Account
	_, err = runtime.local.Submit(ctx, in)
	require.ErrorContains(t, err, "source task is unavailable")
	task := &httpdl.Task{
		ID: testDocument1, FileName: testVideoFile, FileSize: 4, CreatedAt: time.Now(),
		Peer:  &tg.InputPeerChannel{ChannelID: 12, AccessHash: 34},
		Media: &tmedia.Media{Name: testVideoFile, Size: 4, DC: 2, InputFileLoc: &tg.InputDocumentFileLocation{ID: 1}},
	}
	require.NoError(t, runtime.proxy.Tasks().Add(ctx, task))
	in.Dir, in.Out = filepath.Dir(in.FullPath), testVideoFile
	result, err := runtime.local.Submit(ctx, in)
	require.NoError(t, err)
	require.Equal(t, types.DownloadResult{Account: opts.Account, Target: localExecutorName, ID: task.ID}, result)
	record, ok, err := runtime.internal.store.Get(ctx, task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, in.FullPath, record.Path)
	require.Equal(t, InternalDownloadStatusQueued, record.Status)
	// Submission only queues metadata; the worker lifecycle owns file IO.
	require.NoFileExists(t, in.FullPath)
}

package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"

	"github.com/iyear/tdl/core/tmedia"
	"github.com/iyear/tdl/pkg/config"
	"github.com/iyear/tdl/pkg/consts"
)

func TestValidateWatchConfigAllowsInternalModeWithoutAria2(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.HTTP.PublicBaseURL = ""
	cfg.Aria2.RPCURL = ""

	require.NoError(t, validateWatchConfig(cfg))
}

func TestInternalModeUsesFixedDownloadThreads(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.PoolSize = 1

	require.Equal(t, internalDownloadThreads, effectiveDownloadPoolSize(cfg))
}

func TestInternalRuntimeKeepsDownloadTasksWithoutTTL(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.HTTP.DownloadLinkTTLHours = 1

	runtime := newWatchRuntime(cfg, newMemoryTaskStorage(), nil)

	require.Zero(t, runtime.proxy.tasks.ttl)
}

func TestPrepareInternalOutputRootUsesConfiguredWritableDir(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "downloads")
	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.Aria2.Dir = root

	got, fallback, err := prepareInternalOutputRoot(cfg)
	require.NoError(t, err)
	require.False(t, fallback)
	require.Equal(t, filepath.Clean(root), got)
	require.DirExists(t, root)
}

func TestPrepareInternalOutputRootFallsBackWhenDirIsAFile(t *testing.T) {
	oldHome := consts.HomeDir
	consts.HomeDir = t.TempDir()
	defer func() { consts.HomeDir = oldHome }()

	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.Aria2.Dir = blocked

	got, fallback, err := prepareInternalOutputRoot(cfg)
	require.NoError(t, err)
	require.True(t, fallback)
	require.Equal(t, filepath.Join(consts.HomeDir, internalDownloadFallbackDirName), got)
	require.DirExists(t, got)
}

func TestInternalDownloadControllerActions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := newInternalTaskStore(kvd)
	createdAt := time.Now()
	require.NoError(t, store.Save(ctx, internalDownloadRecord{
		ID:        "document_1",
		TaskID:    "document_1",
		FileName:  "video.mp4",
		Total:     100,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: createdAt,
	}))

	controller := NewInternalDownloadController(kvd)
	paused, err := controller.Pause(ctx, []string{"document_1"})
	require.NoError(t, err)
	require.Equal(t, 1, paused.Changed)

	items, err := controller.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, InternalDownloadStatusPaused, items[0].Status)

	started, err := controller.Start(ctx, []string{"document_1"})
	require.NoError(t, err)
	require.Equal(t, 1, started.Changed)

	record, ok, err := store.Get(ctx, "document_1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusQueued, record.Status)

	deleted, err := controller.Delete(ctx, []string{"document_1"})
	require.NoError(t, err)
	require.Equal(t, 1, deleted.Changed)
	_, ok, err = store.Get(ctx, "document_1")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestInternalDownloaderPauseForShutdownUsesNonCanceledContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := newInternalTaskStore(kvd)
	createdAt := time.Now()
	records := []internalDownloadRecord{
		{
			ID:        "active",
			TaskID:    "active",
			FileName:  "active.mp4",
			Total:     100,
			Completed: 40,
			Status:    InternalDownloadStatusActive,
			CreatedAt: createdAt,
		},
		{
			ID:        "queued",
			TaskID:    "queued",
			FileName:  "queued.mp4",
			Total:     100,
			Status:    InternalDownloadStatusQueued,
			CreatedAt: createdAt,
		},
		{
			ID:        "complete",
			TaskID:    "complete",
			FileName:  "complete.mp4",
			Total:     100,
			Completed: 100,
			Status:    InternalDownloadStatusComplete,
			CreatedAt: createdAt,
		},
	}
	for _, record := range records {
		require.NoError(t, store.Save(ctx, record))
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	paused, err := (&internalDownloader{store: store}).PauseForShutdown(canceledCtx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"active", "queued"}, paused)

	active, ok, err := store.Get(ctx, "active")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusPaused, active.Status)
	require.Equal(t, int64(40), active.Completed)

	queued, ok, err := store.Get(ctx, "queued")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusPaused, queued.Status)

	complete, ok, err := store.Get(ctx, "complete")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusComplete, complete.Status)
}

func TestInternalDownloadControllerAddLinkUsesDownloadDirTemplate(t *testing.T) {
	oldHome := consts.HomeDir
	consts.HomeDir = t.TempDir()
	defer func() { consts.HomeDir = oldHome }()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	task := &downloadTask{
		ID:        "document_42",
		PeerID:    12345,
		MessageID: 7,
		Peer:      &tg.InputPeerChannel{ChannelID: 12345, AccessHash: 99},
		FileName:  "video.mp4",
		FileSize:  100,
		CreatedAt: time.Now(),
		Media: &tmedia.Media{
			InputFileLoc: &tg.InputDocumentFileLocation{
				ID:            42,
				AccessHash:    99,
				FileReference: []byte("ref"),
			},
			Name: "video.mp4",
			Size: 100,
			DC:   2,
		},
	}
	require.NoError(t, newTaskStore(kvd, 0).Add(ctx, task))

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.Aria2.Dir = ""
	cfg.DownloadDir = "I/Y&M"

	info, err := NewInternalDownloadController(kvd).AddLink(ctx, cfg, task.ID)
	require.NoError(t, err)
	require.Equal(t, task.ID, info.ID)
	require.Equal(t, InternalDownloadStatusQueued, info.Status)
	require.Equal(t, "video.mp4", filepath.Base(info.Path))
	require.Contains(t, info.Path, filepath.Join(internalDownloadFallbackDirName, "12345", time.Now().Format("200601")))
}

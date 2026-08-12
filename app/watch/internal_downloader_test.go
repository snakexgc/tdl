package watch

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	httpdl "github.com/snakexgc/tdl/app/http"
	"github.com/snakexgc/tdl/app/http/transfer"
	"github.com/snakexgc/tdl/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
	"github.com/snakexgc/tdl/pkg/consts"
)

const (
	testDocument1 = "document_1"
	testActive    = "active"
	testQueued    = "queued"
)

func TestValidateWatchConfigAllowsInternalModeWithoutAria2(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.HTTP.PublicBaseURL = ""
	cfg.Aria2.RPCURL = ""

	require.NoError(t, validateWatchConfig(cfg))
}

func TestValidateWatchConfigDoesNotOwnHTTPServerLifecycle(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeAria2
	cfg.Modules.HTTP = false
	cfg.HTTP.PublicBaseURL = "http://127.0.0.1:22334"

	require.NoError(t, validateWatchConfig(cfg))
}

func TestInternalModeUsesConfiguredPoolSize(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.PoolSize = 3
	cfg.Limit = 2
	opts := DefaultOptions(cfg)

	require.Equal(t, 3, config.EffectivePoolSize(cfg))
	require.Equal(t, 2, effectiveDownloadLimit(cfg))

	runtime := newWatchRuntime(cfg, opts, newMemoryTaskStorage(), nil)
	require.True(t, runtime.proxy.Scheduler() == runtime.internal.scheduler)

	lease, err := runtime.internal.scheduler.Acquire(context.Background(), testDocument1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, lease.Capacity())
	lease.Release()
}

func TestInternalRuntimeKeepsDownloadTasksWithoutTTL(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.HTTP.DownloadLinkTTLHours = 1

	runtime := newWatchRuntime(cfg, DefaultOptions(cfg), newMemoryTaskStorage(), nil)

	require.Zero(t, runtime.proxy.Tasks().TTL())
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
		ID:        testDocument1,
		TaskID:    testDocument1,
		FileName:  testVideoFile,
		Total:     100,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: createdAt,
	}))

	controller := NewInternalDownloadController(kvd)
	paused, err := controller.Pause(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, paused.Changed)

	items, err := controller.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, InternalDownloadStatusPaused, items[0].Status)

	started, err := controller.Start(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, started.Changed)

	record, ok, err := store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusQueued, record.Status)

	deleted, err := controller.Delete(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, deleted.Changed)
	_, ok, err = store.Get(ctx, testDocument1)
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
			ID:        testActive,
			TaskID:    testActive,
			FileName:  "active.mp4",
			Total:     100,
			Completed: 40,
			Status:    InternalDownloadStatusActive,
			CreatedAt: createdAt,
		},
		{
			ID:        testQueued,
			TaskID:    testQueued,
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
	require.ElementsMatch(t, []string{testActive, testQueued}, paused)

	active, ok, err := store.Get(ctx, testActive)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusPaused, active.Status)
	require.Equal(t, int64(40), active.Completed)

	queued, ok, err := store.Get(ctx, testQueued)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusPaused, queued.Status)

	complete, ok, err := store.Get(ctx, "complete")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusComplete, complete.Status)
}

func TestInternalDownloaderRequeuesInterruptedActiveTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := newInternalTaskStore(kvd)
	require.NoError(t, store.Save(ctx, internalDownloadRecord{
		ID:        testDocument1,
		TaskID:    testDocument1,
		FileName:  testVideoFile,
		Total:     100,
		Status:    InternalDownloadStatusActive,
		CreatedAt: time.Now(),
	}))

	downloader := &internalDownloader{store: store}
	require.NoError(t, downloader.requeueInterrupted(ctx))

	record, ok, err := store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusQueued, record.Status)
}

func TestInternalDownloaderKeepsTaskQueuedWhileWaitingForFileSlot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	kvd := newMemoryTaskStorage()
	task := &httpdl.Task{
		ID:        "document_42",
		PeerID:    12345,
		MessageID: 7,
		Peer:      &tg.InputPeerChannel{ChannelID: 12345, AccessHash: 99},
		FileName:  testVideoFile,
		FileSize:  4,
		CreatedAt: time.Now(),
		Media: &tmedia.Media{
			InputFileLoc: &tg.InputDocumentFileLocation{
				ID:            42,
				AccessHash:    99,
				FileReference: []byte("ref"),
			},
			Name: testVideoFile,
			Size: 4,
			DC:   2,
		},
	}
	tasks := httpdl.NewTaskStore(kvd, 0)
	require.NoError(t, tasks.Add(ctx, task))

	store := newInternalTaskStore(kvd)
	target := filepath.Join(t.TempDir(), task.FileName)
	require.NoError(t, store.Save(ctx, internalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       filepath.Dir(target),
		Out:       filepath.Base(target),
		Path:      target,
		Total:     task.FileSize,
		Status:    InternalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}))

	streamCalled := make(chan struct{})
	done := make(chan struct{})
	proxy := httpdl.NewProxy(config.HTTPConfig{}, 1, 2, &httpdl.PoolHolder{}, kvd, nil)
	blockingLease, err := proxy.Scheduler().Acquire(ctx, "other", task.Media.DC)
	require.NoError(t, err)
	proxy.SetStream(func(ctx context.Context, task *httpdl.Task, lease *transfer.TaskLease, start, end int64, w io.Writer) error {
		close(streamCalled)
		_, err := w.Write(bytes.Repeat([]byte("x"), int(end-start+1)))
		return err
	})
	downloader := &internalDownloader{
		proxy:     proxy,
		store:     store,
		scheduler: proxy.Scheduler(),
		logger:    zap.NewNop(),
	}

	go func() {
		downloader.runTask(ctx, task.ID)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	record, ok, err := store.Get(ctx, task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusQueued, record.Status)
	select {
	case <-streamCalled:
		t.Fatal("stream should wait until a file slot is available")
	default:
	}

	blockingLease.Release()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("internal download did not finish after releasing file slot")
	}
	record, ok, err = store.Get(ctx, task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, InternalDownloadStatusComplete, record.Status)
}

func TestInternalDownloadControllerAddLinkUsesDownloadDirTemplate(t *testing.T) {
	oldHome := consts.HomeDir
	consts.HomeDir = t.TempDir()
	defer func() { consts.HomeDir = oldHome }()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	task := &httpdl.Task{
		ID:        "document_42",
		PeerID:    12345,
		MessageID: 7,
		Peer:      &tg.InputPeerChannel{ChannelID: 12345, AccessHash: 99},
		FileName:  testVideoFile,
		FileSize:  100,
		CreatedAt: time.Now(),
		Media: &tmedia.Media{
			InputFileLoc: &tg.InputDocumentFileLocation{
				ID:            42,
				AccessHash:    99,
				FileReference: []byte("ref"),
			},
			Name: testVideoFile,
			Size: 100,
			DC:   2,
		},
	}
	require.NoError(t, httpdl.NewTaskStore(kvd, 0).Add(ctx, task))

	cfg := config.DefaultConfig()
	cfg.Downloader.Mode = config.DownloaderModeInternal
	cfg.Aria2.Dir = ""
	cfg.DownloadDir = "P/Y&M"

	info, err := NewInternalDownloadController(kvd).AddLink(ctx, cfg, task.ID)
	require.NoError(t, err)
	require.Equal(t, task.ID, info.ID)
	require.Equal(t, InternalDownloadStatusQueued, info.Status)
	require.Equal(t, testVideoFile, filepath.Base(info.Path))
	require.Contains(t, info.Path, filepath.Join(internalDownloadFallbackDirName, "12345", time.Now().Format("200601")))
}

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
	local "github.com/snakexgc/tdl/application/downloader.local"
	"github.com/snakexgc/tdl/bsw/cdd/taskhub"
	transfer "github.com/snakexgc/tdl/bsw/ecual/comif"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/internal/core/tmedia"
	"github.com/snakexgc/tdl/pkg/config"
)

const (
	testDocument1 = "document_1"
	testActive    = "active"
	testQueued    = "queued"
)

func TestLocalModeUsesConfiguredPoolSize(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}
	cfg.PoolSize = 3
	cfg.Limit = 2
	opts := DefaultOptions(cfg)

	require.Equal(t, 3, config.EffectivePoolSize(cfg))
	require.Equal(t, 2, config.EffectiveLimit(cfg))

	runtime := newTestWatchRuntime(cfg, opts, newMemoryTaskStorage(), nil)

	lease, err := runtime.proxy.Scheduler().Acquire(context.Background(), testDocument1, 2)
	require.NoError(t, err)
	require.Equal(t, 3, lease.Capacity())
	lease.Release()
}

func TestLocalRuntimeKeepsDownloadTasksWithoutTTL(t *testing.T) {
	t.Parallel()

	cfg := config.DefaultConfig()
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}
	cfg.HTTP.DownloadLinkTTLHours = 1

	runtime := newTestWatchRuntime(cfg, DefaultOptions(cfg), newMemoryTaskStorage(), nil)

	require.Zero(t, runtime.proxy.Tasks().TTL())
}

func TestPrepareLocalOutputRootUsesConfiguredWritableDir(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "downloads")
	cfg := config.DefaultConfig()
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}
	cfg.Downloader.LocalRoot = root

	got, err := local.PrepareRoot(cfg.Downloader.LocalRoot)
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(root), got)
	require.DirExists(t, root)
}

func TestPrepareLocalOutputRootRejectsInvalidDirectory(t *testing.T) {
	t.Parallel()
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(blocked, []byte("x"), 0o644))
	for _, root := range []string{blocked, "", "relative"} {
		actual, err := local.PrepareRoot(root)
		require.Error(t, err)
		require.Empty(t, actual)
	}
}

func TestLocalDownloadControllerActions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := taskhub.NewLocalRepository(kvd)
	createdAt := time.Now()
	require.NoError(t, store.Save(ctx, types.LocalDownloadRecord{
		ID:        testDocument1,
		TaskID:    testDocument1,
		FileName:  testVideoFile,
		Total:     100,
		Status:    types.LocalDownloadStatusQueued,
		CreatedAt: createdAt,
	}))

	controller := local.NewController(taskhub.NewLocalRepository(kvd))
	paused, err := controller.Pause(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, paused.Changed)

	items, err := controller.List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, types.LocalDownloadStatusPaused, items[0].Status)

	started, err := controller.Start(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, started.Changed)

	record, ok, err := store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusQueued, record.Status)

	deleted, err := controller.Delete(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.Equal(t, 1, deleted.Changed)
	_, ok, err = store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestLocalDownloadControllerKeepsRecordWhenFileDeleteFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := taskhub.NewLocalRepository(kvd)
	nonEmptyDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(nonEmptyDir, "child"), []byte("x"), 0o644))
	require.NoError(t, store.Save(ctx, types.LocalDownloadRecord{
		ID:        testDocument1,
		TaskID:    testDocument1,
		Path:      nonEmptyDir,
		Status:    types.LocalDownloadStatusQueued,
		CreatedAt: time.Now(),
	}))

	result, err := local.NewController(taskhub.NewLocalRepository(kvd)).Delete(ctx, []string{testDocument1})
	require.NoError(t, err)
	require.NotEmpty(t, result.Errors)
	require.Zero(t, result.Changed)
	_, ok, err := store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestLocalDownloaderPauseForShutdownUsesNonCanceledContext(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := taskhub.NewLocalRepository(kvd)
	createdAt := time.Now()
	records := []types.LocalDownloadRecord{
		{
			ID:        testActive,
			TaskID:    testActive,
			FileName:  "active.mp4",
			Total:     100,
			Completed: 40,
			Status:    types.LocalDownloadStatusActive,
			CreatedAt: createdAt,
		},
		{
			ID:        testQueued,
			TaskID:    testQueued,
			FileName:  "queued.mp4",
			Total:     100,
			Status:    types.LocalDownloadStatusQueued,
			CreatedAt: createdAt,
		},
		{
			ID:        "complete",
			TaskID:    "complete",
			FileName:  "complete.mp4",
			Total:     100,
			Completed: 100,
			Status:    types.LocalDownloadStatusComplete,
			CreatedAt: createdAt,
		},
	}
	for _, record := range records {
		require.NoError(t, store.Save(ctx, record))
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	paused, err := local.New(nil, store, nil).PauseForShutdown(canceledCtx)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{testActive, testQueued}, paused)

	active, ok, err := store.Get(ctx, testActive)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusPaused, active.Status)
	require.Equal(t, int64(40), active.Completed)

	queued, ok, err := store.Get(ctx, testQueued)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusPaused, queued.Status)

	complete, ok, err := store.Get(ctx, "complete")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusComplete, complete.Status)
}

func TestLocalDownloaderRequeuesInterruptedActiveTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := taskhub.NewLocalRepository(kvd)
	require.NoError(t, store.Save(ctx, types.LocalDownloadRecord{
		ID:        testDocument1,
		TaskID:    testDocument1,
		FileName:  testVideoFile,
		Total:     100,
		Status:    types.LocalDownloadStatusActive,
		CreatedAt: time.Now(),
	}))

	downloader := local.New(nil, store, nil)
	require.NoError(t, downloader.Recover(ctx))

	record, ok, err := store.Get(ctx, testDocument1)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusQueued, record.Status)
}

func TestLocalDownloaderKeepsTaskQueuedWhileWaitingForFileSlot(t *testing.T) {
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

	store := taskhub.NewLocalRepository(kvd)
	target := filepath.Join(t.TempDir(), task.FileName)
	require.NoError(t, store.Save(ctx, types.LocalDownloadRecord{
		ID:        task.ID,
		TaskID:    task.ID,
		FileName:  task.FileName,
		Dir:       filepath.Dir(target),
		Out:       filepath.Base(target),
		Path:      target,
		Total:     task.FileSize,
		Status:    types.LocalDownloadStatusQueued,
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
	downloader := local.New(localSource{proxy: proxy, scheduler: proxy.Scheduler()}, store, zap.NewNop())

	go func() {
		downloader.Execute(ctx, task.ID)
		close(done)
	}()

	time.Sleep(150 * time.Millisecond)
	record, ok, err := store.Get(ctx, task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusQueued, record.Status)
	select {
	case <-streamCalled:
		t.Fatal("stream should wait until a file slot is available")
	default:
	}

	blockingLease.Release()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("local download did not finish after releasing file slot")
	}
	record, ok, err = store.Get(ctx, task.ID)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusComplete, record.Status)
}

func TestLocalDownloadControllerAddLinkUsesDownloadDirTemplate(t *testing.T) {
	t.Parallel()

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
	cfg.Downloader.Executors = []string{config.DownloadExecutorLocal}
	cfg.Downloader.LocalRoot = filepath.Join(t.TempDir(), "downloads")
	cfg.Aria2.Dir = ""
	cfg.DownloadDir = "P/Y&M"

	policies, _, naming, err := startPolicies(ctx, cfg.Namespace, DefaultOptions(cfg))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, policies.Stop(ctx)) })
	_, err = (local.SavedLinks{Account: types.AccountID(cfg.Namespace), Root: cfg.Downloader.LocalRoot, Naming: naming, Source: httpdl.NewTaskStore(kvd, 0), Repository: taskhub.NewLocalRepository(kvd)}).Submit(ctx, types.DownloadSubmission{Account: types.AccountID(cfg.Namespace), TaskID: task.ID})
	require.NoError(t, err)
	items, err := local.NewController(taskhub.NewLocalRepository(kvd)).List(ctx)
	require.NoError(t, err)
	require.Len(t, items, 1)
	info := items[0]
	require.Equal(t, task.ID, info.ID)
	require.Equal(t, types.LocalDownloadStatusQueued, info.Status)
	require.Equal(t, testVideoFile, filepath.Base(info.Path))
	require.Contains(t, info.Path, filepath.Join("downloads", "12345", time.Now().Format("200601")))
}

package local

import (
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/rte"
)

const testDeleted = "deleted"

const testTask = "local-task"

type memoryRepository struct {
	mu         sync.Mutex
	records    map[string]types.LocalDownloadRecord
	downloaded bool
}

func newRepository() *memoryRepository {
	return &memoryRepository{records: map[string]types.LocalDownloadRecord{}}
}

func (r *memoryRepository) Create(_ context.Context, record types.LocalDownloadRecord) (types.LocalDownloadRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if current, ok := r.records[record.ID]; ok {
		return current, nil
	}
	r.records[record.ID] = record
	return record, nil
}

func (r *memoryRepository) Save(_ context.Context, record types.LocalDownloadRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records[record.ID] = record
	return nil
}

func (r *memoryRepository) Get(_ context.Context, id string) (types.LocalDownloadRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.records[id]
	return v, ok, nil
}

func (r *memoryRepository) Records(context.Context) (map[string]types.LocalDownloadRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := map[string]types.LocalDownloadRecord{}
	for id, v := range r.records {
		result[id] = v
	}
	return result, nil
}

func (r *memoryRepository) Remove(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.records, id)
	return nil
}

func (r *memoryRepository) Update(_ context.Context, id string, fn func(*types.LocalDownloadRecord) bool) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.records[id]
	if !ok || !fn(&v) {
		return false, nil
	}
	r.records[id] = v
	return true, nil
}

func (r *memoryRepository) MarkDownloaded(context.Context, string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.downloaded = true
	return nil
}

type source struct {
	stream func(context.Context, io.Writer) error
}

func (source) Get(_ context.Context, id string) (types.LocalDownloadSource, bool, error) {
	return types.LocalDownloadSource{ID: id, FileSize: 4, DC: 2, Available: true}, true, nil
}

type lease struct{}

func (lease) Release()                                                           {}
func (source) Acquire(context.Context, string, int) (ports.DownloadLease, error) { return lease{}, nil }

func (s source) Stream(ctx context.Context, _ string, _ ports.DownloadLease, _, _ int64, w io.Writer) error {
	return s.stream(ctx, w)
}

func TestLateReportsCannotRestoreDeletedOrPausedTasks(t *testing.T) {
	ctx := context.Background()
	for _, status := range []string{types.LocalDownloadStatusPaused, types.LocalDownloadStatusRemoved, testDeleted} {
		t.Run(status, func(t *testing.T) {
			repo := newRepository()
			stale := types.LocalDownloadRecord{ID: testTask, TaskID: testTask, Status: types.LocalDownloadStatusActive, Total: 4}
			if status != testDeleted {
				current := stale
				current.Status = status
				require.NoError(t, repo.Save(ctx, current))
			}
			worker := New(nil, repo, nil)
			writer := &internalProgressWriter{ctx: ctx, store: repo, id: testTask, total: 4, completed: 4, speed: &speedCalc{}}
			writer.flush()
			worker.markComplete(ctx, stale)
			worker.markError(ctx, stale, io.ErrUnexpectedEOF)
			current, ok, err := repo.Get(ctx, testTask)
			require.NoError(t, err)
			require.Equal(t, status != testDeleted, ok)
			if ok {
				require.Equal(t, status, current.Status)
				require.Zero(t, current.Completed)
			}
			require.False(t, repo.downloaded)
		})
	}
}

func TestShortStreamDoesNotCompleteTask(t *testing.T) {
	repo := newRepository()
	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, repo.Save(context.Background(), types.LocalDownloadRecord{ID: testTask, TaskID: testTask, Path: path, Dir: filepath.Dir(path), Status: types.LocalDownloadStatusQueued, Total: 4}))
	worker := New(source{stream: func(_ context.Context, w io.Writer) error { _, err := w.Write([]byte("x")); return err }}, repo, nil)
	worker.Execute(context.Background(), testTask)
	record, ok, err := repo.Get(context.Background(), testTask)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, types.LocalDownloadStatusError, record.Status)
	require.Contains(t, record.Error, "unexpected EOF")
	require.False(t, repo.downloaded)
}

func TestLateReportsCannotModifyRecreatedOrRestartedTask(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	later := now.Add(time.Second)
	stale := types.LocalDownloadRecord{ID: testTask, TaskID: testTask, CreatedAt: now, StartedAt: &now, Status: types.LocalDownloadStatusActive, Total: 4}
	for _, restart := range []bool{false, true} {
		repo := newRepository()
		current := stale
		if restart {
			current.StartedAt = &later
		} else {
			current.CreatedAt = later
		}
		require.NoError(t, repo.Save(ctx, current))
		worker := New(nil, repo, nil)
		writer := &internalProgressWriter{record: stale, ctx: ctx, store: repo, id: testTask, total: 4, completed: 4, speed: &speedCalc{}}
		require.ErrorIs(t, writer.checkStatus(), types.ErrLocalDownloadRemoved)
		writer.flush()
		worker.markComplete(ctx, stale)
		worker.markError(ctx, stale, io.ErrUnexpectedEOF)
		got, ok, err := repo.Get(ctx, testTask)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, current, got)
		require.False(t, repo.downloaded)
	}
}

func TestComponentShutdownWaitsForStreamBeforeReleasingSource(t *testing.T) {
	repo := newRepository()
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	worker := New(source{stream: func(ctx context.Context, _ io.Writer) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}}, repo, nil)
	registry := rte.NewRegistry()
	require.NoError(t, Register(registry, worker))
	host, err := registry.Build("alice", nil, nil)
	require.NoError(t, err)
	require.Equal(t, rte.Running, host.Start(context.Background())[0].State)
	value, err := host.Resolve(ports.DownloadExecutorName)
	require.NoError(t, err)
	executor := value.(ports.DownloadExecutor)
	in := types.DownloadSubmission{Account: "bob", TaskID: testTask, FullPath: filepath.Join(t.TempDir(), "file")}
	_, err = executor.Submit(context.Background(), in)
	require.ErrorContains(t, err, "account mismatch")
	in.Account = "alice"
	in.Dir = filepath.Dir(in.FullPath)
	_, err = executor.Submit(context.Background(), in)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, host.Stop(stopCtx), context.DeadlineExceeded)
	<-canceled
	close(release)
	require.NoError(t, host.Stop(context.Background()))
	_, err = executor.Submit(context.Background(), in)
	require.ErrorContains(t, err, "stopped")
}

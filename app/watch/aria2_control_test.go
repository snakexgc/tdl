package watch

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAria2ControllerOverviewCountsOwnedRemainingAndRetryableTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newAria2TaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         "registered-error",
		TaskID:      "document_1",
		DownloadURL: "http://127.0.0.1:8080/download/document_1",
		CreatedAt:   time.Now(),
	}))

	controller := &Aria2Controller{
		client: &fakeAria2ControlClient{
			active: []aria2DownloadStatus{
				{
					GID:             "url-active",
					Status:          "active",
					TotalLength:     "100",
					CompletedLength: "40",
					Files:           filesWithURI("http://127.0.0.1:8080/download/document_2"),
				},
				{
					GID:             "user-active",
					Status:          "active",
					TotalLength:     "999",
					CompletedLength: "0",
					Files:           filesWithURI("http://example.com/file"),
				},
			},
			waiting: []aria2DownloadStatus{
				{
					GID:             "url-paused",
					Status:          "paused",
					TotalLength:     "50",
					CompletedLength: "10",
					Files:           filesWithURI("http://127.0.0.1:8080/download/document_3"),
				},
			},
			stopped: []aria2DownloadStatus{
				{
					GID:             "registered-error",
					Status:          "error",
					TotalLength:     "200",
					CompletedLength: "80",
				},
				{
					GID:             "url-complete",
					Status:          "complete",
					TotalLength:     "30",
					CompletedLength: "30",
					Files:           filesWithURI("http://127.0.0.1:8080/download/document_4"),
				},
			},
		},
		store:         store,
		publicBaseURL: "http://127.0.0.1:8080",
		logger:        zap.NewNop(),
	}

	overview, err := controller.Overview(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, overview.TotalTasks)
	require.Equal(t, 3, overview.RemainingTasks)
	require.Equal(t, int64(220), overview.RemainingBytes)
	require.Equal(t, 1, overview.StatusCounts["active"])
	require.Equal(t, 1, overview.StatusCounts["paused"])
	require.Equal(t, 1, overview.StatusCounts["error"])
	require.Equal(t, 1, overview.StatusCounts["complete"])
	require.Len(t, overview.RetryCandidates, 1)
	require.Equal(t, "registered-error", overview.RetryCandidates[0].GID)
	require.Equal(t, int64(120), overview.RetryBytes)
}

func TestAria2ControllerPauseStartAndRetryOnlyOwnedTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := newAria2TaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         "registered-error",
		TaskID:      "document_1",
		DownloadURL: "http://127.0.0.1:8080/download/document_1",
		Dir:         "downloads",
		Out:         "video.mp4",
		Connections: 6,
		CreatedAt:   time.Now(),
	}))

	client := &fakeAria2ControlClient{
		active: []aria2DownloadStatus{
			{GID: "url-active", Status: "active", Files: filesWithURI("http://127.0.0.1:8080/download/document_2")},
		},
		waiting: []aria2DownloadStatus{
			{GID: "url-waiting", Status: "waiting", Files: filesWithURI("http://127.0.0.1:8080/download/document_3")},
			{GID: "url-paused", Status: "paused", Files: filesWithURI("http://127.0.0.1:8080/download/document_4")},
		},
		stopped: []aria2DownloadStatus{
			{
				GID:             "registered-error",
				Status:          "error",
				TotalLength:     "200",
				CompletedLength: "100",
			},
			{GID: "user-error", Status: "error", Files: filesWithURI("http://example.com/file")},
		},
		addedGID: "new-gid",
	}
	controller := &Aria2Controller{
		client:        client,
		store:         store,
		publicBaseURL: "http://127.0.0.1:8080",
		logger:        zap.NewNop(),
	}

	paused, err := controller.PauseAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, paused.Matched)
	require.Equal(t, 2, paused.Changed)
	require.Equal(t, []string{"url-active", "url-waiting"}, client.forcePaused)

	started, err := controller.StartAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, started.Matched)
	require.Equal(t, 1, started.Changed)
	require.Equal(t, []string{"url-paused"}, client.unpaused)

	retried, err := controller.RetryStopped(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, retried.Matched)
	require.Equal(t, 1, retried.Changed)
	require.Equal(t, []string{"http://127.0.0.1:8080/download/document_1"}, client.addedURIs)
	require.Equal(t, []aria2AddURIOptions{{Dir: "downloads", Out: "video.mp4", Connections: 6}}, client.addedOptions)
	require.Equal(t, []string{"registered-error"}, client.removedResults)

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.NotContains(t, records, "registered-error")
	require.Contains(t, records, "new-gid")
}

type fakeAria2ControlClient struct {
	active         []aria2DownloadStatus
	waiting        []aria2DownloadStatus
	stopped        []aria2DownloadStatus
	forcePaused    []string
	unpaused       []string
	removedResults []string
	addedURIs      []string
	addedOptions   []aria2AddURIOptions
	addedGID       string
}

func (f *fakeAria2ControlClient) TellActive(ctx context.Context) ([]aria2DownloadStatus, error) {
	return f.active, nil
}

func (f *fakeAria2ControlClient) TellWaiting(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error) {
	if offset >= len(f.waiting) {
		return nil, nil
	}
	end := offset + num
	if end > len(f.waiting) {
		end = len(f.waiting)
	}
	return f.waiting[offset:end], nil
}

func (f *fakeAria2ControlClient) TellStopped(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error) {
	if offset >= len(f.stopped) {
		return nil, nil
	}
	end := offset + num
	if end > len(f.stopped) {
		end = len(f.stopped)
	}
	return f.stopped[offset:end], nil
}

func (f *fakeAria2ControlClient) ForcePause(ctx context.Context, gid string) error {
	f.forcePaused = append(f.forcePaused, gid)
	return nil
}

func (f *fakeAria2ControlClient) Unpause(ctx context.Context, gid string) error {
	f.unpaused = append(f.unpaused, gid)
	return nil
}

func (f *fakeAria2ControlClient) AddURI(ctx context.Context, uri string, opts aria2AddURIOptions) (string, error) {
	f.addedURIs = append(f.addedURIs, uri)
	f.addedOptions = append(f.addedOptions, opts)
	if f.addedGID == "" {
		return "new-gid", nil
	}
	return f.addedGID, nil
}

func (f *fakeAria2ControlClient) RemoveDownloadResult(ctx context.Context, gid string) error {
	f.removedResults = append(f.removedResults, gid)
	return nil
}

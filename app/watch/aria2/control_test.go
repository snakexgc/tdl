package aria2

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/iyear/tdl/pkg/config"
)

const (
	testGIDRegisteredError = "registered-error"
	testDocument1          = "document_1"
	testDownloadURL1       = "http://127.0.0.1:8080/download/document_1"
	testGIDURLActive       = "url-active"
	testGIDURLPaused       = "url-paused"
	testGIDURLWaiting      = "url-waiting"
	testGIDNew             = "new-gid"
)

func TestAria2ControllerOverviewCountsOwnedRemainingAndRetryableTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         testGIDRegisteredError,
		TaskID:      testDocument1,
		DownloadURL: testDownloadURL1,
		CreatedAt:   time.Now(),
	}))

	controller := &Aria2Controller{
		client: &fakeAria2ControlClient{
			active: []aria2DownloadStatus{
				{
					GID:             testGIDURLActive,
					Status:          aria2StatusActive,
					TotalLength:     "100",
					CompletedLength: "40",
					Files:           filesWithURI("http://127.0.0.1:8080/download/document_2"),
				},
				{
					GID:             "user-active",
					Status:          aria2StatusActive,
					TotalLength:     "999",
					CompletedLength: "0",
					Files:           filesWithURI("http://example.com/file"),
				},
			},
			waiting: []aria2DownloadStatus{
				{
					GID:             testGIDURLPaused,
					Status:          aria2StatusPaused,
					TotalLength:     "50",
					CompletedLength: "10",
					Files:           filesWithURI("http://127.0.0.1:8080/download/document_3"),
				},
			},
			stopped: []aria2DownloadStatus{
				{
					GID:             testGIDRegisteredError,
					Status:          aria2StatusError,
					TotalLength:     "200",
					CompletedLength: "80",
				},
				{
					GID:             "url-complete",
					Status:          aria2StatusComplete,
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
	require.Equal(t, 1, overview.StatusCounts[aria2StatusActive])
	require.Equal(t, 1, overview.StatusCounts[aria2StatusPaused])
	require.Equal(t, 1, overview.StatusCounts[aria2StatusError])
	require.Equal(t, 1, overview.StatusCounts[aria2StatusComplete])
	require.Len(t, overview.RetryCandidates, 1)
	require.Equal(t, testGIDRegisteredError, overview.RetryCandidates[0].GID)
	require.Equal(t, int64(120), overview.RetryBytes)
}

func TestAria2ControllerPauseStartAndRetryOnlyOwnedTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewTaskStore(newMemoryTaskStorage())
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         testGIDRegisteredError,
		TaskID:      testDocument1,
		DownloadURL: testDownloadURL1,
		Dir:         testDownloadDir,
		Out:         "video.mp4",
		Connections: 6,
		CreatedAt:   time.Now(),
	}))

	client := &fakeAria2ControlClient{
		active: []aria2DownloadStatus{
			{GID: testGIDURLActive, Status: aria2StatusActive, Files: filesWithURI("http://127.0.0.1:8080/download/document_2")},
		},
		waiting: []aria2DownloadStatus{
			{GID: testGIDURLWaiting, Status: aria2StatusWaiting, Files: filesWithURI("http://127.0.0.1:8080/download/document_3")},
			{GID: testGIDURLPaused, Status: aria2StatusPaused, Files: filesWithURI("http://127.0.0.1:8080/download/document_4")},
		},
		stopped: []aria2DownloadStatus{
			{
				GID:             testGIDRegisteredError,
				Status:          aria2StatusError,
				TotalLength:     "200",
				CompletedLength: "100",
			},
			{GID: "user-error", Status: aria2StatusError, Files: filesWithURI("http://example.com/file")},
		},
		addedGID: testGIDNew,
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
	require.Equal(t, []string{testGIDURLActive, testGIDURLWaiting}, client.forcePaused)

	started, err := controller.StartAll(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, started.Matched)
	require.Equal(t, 1, started.Changed)
	require.Equal(t, []string{testGIDURLPaused}, client.unpaused)

	retried, err := controller.RetryStopped(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, retried.Matched)
	require.Equal(t, 1, retried.Changed)
	require.Equal(t, []string{testDownloadURL1}, client.addedURIs)
	require.Equal(t, []aria2AddURIOptions{{Dir: testDownloadDir, Out: "video.mp4", Connections: 1}}, client.addedOptions)
	require.Equal(t, []string{testGIDRegisteredError}, client.removedResults)

	records, err := store.Records(ctx)
	require.NoError(t, err)
	require.NotContains(t, records, testGIDRegisteredError)
	require.Contains(t, records, testGIDNew)
}

func TestTaskRecordHTTPConnectionsRequiresClientRangeMode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 1, taskRecordHTTPConnections(TaskRecord{Connections: 6}))
	require.Equal(t, 6, taskRecordHTTPConnections(TaskRecord{
		Connections:  6,
		TransferMode: config.HTTPTransferModeClientRange,
	}))
}

func TestTaskNamePrefersBittorrentInfoPathThenURI(t *testing.T) {
	t.Parallel()

	require.Equal(t, "Ubuntu ISO", TaskName(DownloadStatus{
		Bittorrent: &BT{Info: &BTInfo{Name: "Ubuntu ISO"}},
		Files:      filesWithURI("http://example.com/ignored.iso"),
	}))
	require.Equal(t, "file.bin", TaskName(DownloadStatus{
		Files: []File{{Path: "/downloads/file.bin"}},
	}))
	require.Equal(t, "from-uri.mkv", TaskName(DownloadStatus{
		Files: filesWithURI("https://example.com/videos/from-uri.mkv"),
	}))
}

type fakeAria2ControlClient struct {
	active         []aria2DownloadStatus
	waiting        []aria2DownloadStatus
	stopped        []aria2DownloadStatus
	globalOptions  map[string]string
	forcePaused    []string
	paused         []string
	unpaused       []string
	removed        []string
	removedResults []string
	addedURIs      []string
	addedOptions   []aria2AddURIOptions
	addedGID       string
}

func (f *fakeAria2ControlClient) GetGlobalOptions(ctx context.Context) (map[string]string, error) {
	if f.globalOptions == nil {
		return map[string]string{}, nil
	}
	return f.globalOptions, nil
}

func (f *fakeAria2ControlClient) TellStatus(ctx context.Context, gid string) (aria2DownloadStatus, error) {
	for _, task := range append(append([]aria2DownloadStatus{}, f.active...), append(f.waiting, f.stopped...)...) {
		if task.GID == gid {
			return task, nil
		}
	}
	return aria2DownloadStatus{}, nil
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

func (f *fakeAria2ControlClient) Pause(ctx context.Context, gid string) error {
	f.paused = append(f.paused, gid)
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
		return testGIDNew, nil
	}
	return f.addedGID, nil
}

func (f *fakeAria2ControlClient) Remove(ctx context.Context, gid string) error {
	f.removed = append(f.removed, gid)
	return nil
}

func (f *fakeAria2ControlClient) RemoveDownloadResult(ctx context.Context, gid string) error {
	f.removedResults = append(f.removedResults, gid)
	return nil
}

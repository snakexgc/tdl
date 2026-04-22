package watch

import (
	"context"
	"testing"
	"time"

	gferrors "github.com/go-faster/errors"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSuspendTDLAria2TasksForReconnectOnlyPausesOwnedTasks(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	kvd := newMemoryTaskStorage()
	store := newAria2TaskStore(kvd)
	require.NoError(t, store.Add(ctx, aria2TaskRecord{
		GID:         "registered-active",
		TaskID:      "document_1",
		DownloadURL: "http://127.0.0.1:8080/base/download/document_1",
		CreatedAt:   time.Now(),
	}))

	client := &fakeAria2ReconnectClient{
		active: []aria2DownloadStatus{
			{GID: "registered-active", Status: "active"},
			{GID: "url-active", Status: "active", Files: filesWithURI("http://127.0.0.1:8080/base/download/document_2")},
			{GID: "user-active", Status: "active", Files: filesWithURI("http://example.com/file")},
		},
		waiting: []aria2DownloadStatus{
			{GID: "url-waiting", Status: "waiting", Files: filesWithURI("http://127.0.0.1:8080/base/download/document_3")},
			{GID: "user-waiting", Status: "waiting", Files: filesWithURI("http://example.com/waiting")},
			{GID: "paused-tdl", Status: "paused", Files: filesWithURI("http://127.0.0.1:8080/base/download/document_4")},
		},
	}

	paused, err := suspendTDLAria2TasksForReconnect(ctx, client, store, "http://127.0.0.1:8080/base", zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, []string{"registered-active", "url-active", "url-waiting"}, paused)
	require.Equal(t, paused, client.forcePaused)
}

func TestResumeTDLAria2TasksOnlyResumesUniquePausedGIDs(t *testing.T) {
	t.Parallel()

	client := &fakeAria2ReconnectClient{}
	err := resumeTDLAria2Tasks(context.Background(), client, []string{"gid-1", "gid-2", "gid-1"}, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, []string{"gid-1", "gid-2"}, client.unpaused)
}

func TestRetryAria2ConnectionRetriesConnectionErrorOnly(t *testing.T) {
	t.Parallel()

	calls := 0
	err := retryAria2ConnectionWithInterval(context.Background(), zap.NewNop(), "test", time.Millisecond, func() error {
		calls++
		if calls == 1 {
			return fakeAria2ConnectionError()
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)

	calls = 0
	err = retryAria2ConnectionWithInterval(context.Background(), zap.NewNop(), "test", time.Millisecond, func() error {
		calls++
		return gferrors.New("aria2 rpc error 1: not found")
	})
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func filesWithURI(uri string) []aria2File {
	return []aria2File{{URIs: []aria2URI{{URI: uri}}}}
}

type fakeAria2ReconnectClient struct {
	active      []aria2DownloadStatus
	waiting     []aria2DownloadStatus
	forcePaused []string
	unpaused    []string
	forceErrs   map[string]error
	unpauseErrs map[string]error
}

func (f *fakeAria2ReconnectClient) TellActive(ctx context.Context) ([]aria2DownloadStatus, error) {
	return f.active, nil
}

func (f *fakeAria2ReconnectClient) TellWaiting(ctx context.Context, offset, num int) ([]aria2DownloadStatus, error) {
	if offset >= len(f.waiting) {
		return nil, nil
	}
	end := offset + num
	if end > len(f.waiting) {
		end = len(f.waiting)
	}
	return f.waiting[offset:end], nil
}

func (f *fakeAria2ReconnectClient) ForcePause(ctx context.Context, gid string) error {
	if err := f.forceErrs[gid]; err != nil {
		return err
	}
	f.forcePaused = append(f.forcePaused, gid)
	return nil
}

func (f *fakeAria2ReconnectClient) Unpause(ctx context.Context, gid string) error {
	if err := f.unpauseErrs[gid]; err != nil {
		return err
	}
	f.unpaused = append(f.unpaused, gid)
	return nil
}

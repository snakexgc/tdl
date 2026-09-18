package downloadcontrol

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/ports"
	"github.com/snakexgc/tdl/interfaces/types"
)

type backendStub struct {
	items  []types.DownloadTask
	reads  int
	action string
	ids    []string
}

func (b *backendStub) ListTasks(context.Context) ([]types.DownloadTask, error) {
	b.reads++
	return b.items, nil
}

func (b *backendStub) ChangeTasks(_ context.Context, action string, ids []string) (types.DownloadActionResult, error) {
	b.action = action
	b.ids = ids
	return types.DownloadActionResult{Changed: len(ids)}, nil
}

func TestAccountValidationPrecedesBackendAccess(t *testing.T) {
	b := &backendStub{}
	s := New("a", map[string]ports.DownloadBackend{localExecutor: b})
	_, err := s.Tasks(context.Background(), "b", localExecutor)
	require.ErrorContains(t, err, "account mismatch")
	_, err = s.Control(context.Background(), types.DownloadAction{Account: "b", Executor: localExecutor, Action: "delete_all"})
	require.Error(t, err)
	require.Zero(t, b.reads)
	require.Empty(t, b.action)
	_, err = s.Control(context.Background(), types.DownloadAction{Account: "a", Executor: localExecutor, Action: "invalid_all"})
	require.Error(t, err)
	require.Zero(t, b.reads)
}

func TestSafeBulkSelectionAndDeduplication(t *testing.T) {
	b := &backendStub{items: []types.DownloadTask{{ID: statusActive, Status: statusActive}, {ID: "done", Status: statusComplete}, {ID: "failed", Status: statusError}, {ID: statusPaused, Status: statusPaused}}}
	s := New("a", map[string]ports.DownloadBackend{localExecutor: b})
	_, err := s.Control(context.Background(), types.DownloadAction{Account: "a", Executor: localExecutor, Action: "delete_all"})
	require.NoError(t, err)
	require.Equal(t, []string{"done", "failed"}, b.ids)
	_, err = s.Control(context.Background(), types.DownloadAction{Account: "a", Executor: localExecutor, Action: "start", IDs: []string{" paused ", statusPaused, ""}})
	require.NoError(t, err)
	require.Equal(t, actionResume, b.action)
	require.Equal(t, []string{statusPaused}, b.ids)
	items, err := s.Tasks(context.Background(), "a", localExecutor)
	require.NoError(t, err)
	require.Equal(t, types.AccountID("a"), items[0].Account)
	require.Empty(t, b.items[0].Account, "backend records must not be mutated")
}

func TestUniformReportsPreserveBackendStatus(t *testing.T) {
	for _, status := range []string{statusQueued, statusWaiting} {
		b := &backendStub{items: []types.DownloadTask{{ID: "task", Status: status}}}
		s := New("a", map[string]ports.DownloadBackend{localExecutor: b})
		items, err := s.Tasks(context.Background(), "a", localExecutor)
		require.NoError(t, err)
		require.Equal(t, types.DownloadQueued, items[0].State)
		require.Equal(t, status, items[0].Status)
		require.Empty(t, b.items[0].State)
	}
	require.Equal(t, types.DownloadUnknown, normalizedState("new-backend-status"))
}

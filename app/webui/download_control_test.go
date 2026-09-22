package webui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/snakexgc/tdl/interfaces/types"
	"github.com/snakexgc/tdl/pkg/config"
)

const testScopedAccount = "scoped"

type downloadControlStub struct {
	account types.AccountID
	request types.DownloadAction
	calls   int
}

func (d *downloadControlStub) Tasks(_ context.Context, executor string) ([]types.DownloadTask, error) {
	d.calls++
	return []types.DownloadTask{{Account: d.account, Executor: executor, ID: "example", Status: "paused"}}, nil
}

func (d *downloadControlStub) Control(_ context.Context, request types.DownloadAction) (types.DownloadActionResult, error) {
	d.calls++
	d.request = request
	return types.DownloadActionResult{Changed: 1}, nil
}

func TestDownloadControlAPIUsesPortAndRejectsAccountOverride(t *testing.T) {
	initWebUITestConfig(t)
	port := &downloadControlStub{}
	s := NewServer(Options{Namespace: "a", DownloadControl: port})
	r := httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(`{"executor":"local","action":"pause","ids":["example"]}`))
	w := httptest.NewRecorder()
	s.routes().ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.Zero(t, port.calls)
	r = httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(`{"executor":"local","action":"pause","ids":["example"]}`))
	w = httptest.NewRecorder()
	s.handleDownloadTaskActions(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, types.AccountID("a"), port.request.Account)
	for _, body := range []string{`{"account":"other","executor":"local","action":"delete_all"}`, `{} {}`, `{"unknown":true}`} {
		r = httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(body))
		w = httptest.NewRecorder()
		s.handleDownloadTaskActions(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	require.Equal(t, 1, port.calls)
	r = httptest.NewRequest(http.MethodGet, "/api/download-tasks?executor=local", nil)
	w = httptest.NewRecorder()
	s.handleDownloadTasks(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "example")
}

func TestDownloadTaskObservationUsesTheCurrentSessionPort(t *testing.T) {
	port := &downloadControlStub{account: testScopedAccount}
	s := NewServer(Options{Namespace: testScopedAccount, DownloadControl: port})
	for _, executor := range []string{localDownloadExecutor, config.DownloadExecutorAria2} {
		source := s.snapshotSource("download-tasks-" + executor)
		require.NotNil(t, source)
		data, err := source(context.Background())
		require.NoError(t, err)
		items := data.(map[string]any)[fieldItems].([]types.DownloadTask)
		require.Len(t, items, 1)
		require.Equal(t, types.AccountID(testScopedAccount), items[0].Account)
		require.Equal(t, executor, items[0].Executor)
	}
	require.Nil(t, s.snapshotSource("downloads"), "removed observation topic must not be accepted")
	require.Nil(t, s.snapshotSource("download-tasks-other"))
}

func TestMissingComponentPortsRemainUnavailable(t *testing.T) {
	s := NewServer(Options{Namespace: testScopedAccount})
	list := httptest.NewRecorder()
	s.handleDownloadTasks(list, httptest.NewRequest(http.MethodGet, "/api/download-tasks", nil))
	require.Equal(t, http.StatusServiceUnavailable, list.Code)
	action := httptest.NewRecorder()
	s.handleDownloadTaskActions(action, httptest.NewRequest(http.MethodPost, "/api/download-tasks/actions", strings.NewReader(`{"executor":"local","action":"delete_all"}`)))
	require.Equal(t, http.StatusServiceUnavailable, action.Code)
	_, err := s.downloadTasksSnapshot("local")(context.Background())
	require.ErrorContains(t, err, "unavailable")
	request := httptest.NewRequest(http.MethodGet, "/api/update/check", nil)
	_, err = s.checkUpdate(request)
	require.ErrorContains(t, err, "unavailable")
	_, _, err = s.downloadUpdate(request, "selected-version")
	require.ErrorContains(t, err, "unavailable")
}
